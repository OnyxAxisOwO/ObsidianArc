package sandbox

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// fakeExecutor records what it was asked to run and answers with a fixed
// result, so the broker's gating can be tested without an interpreter.
type fakeExecutor struct {
	mu     sync.Mutex
	jobs   []Job
	result Result
	err    error
	block  chan struct{}
	inside atomic.Int32
}

func (e *fakeExecutor) Run(ctx context.Context, _ Profile, job Job) (Result, error) {
	e.mu.Lock()
	e.jobs = append(e.jobs, job)
	e.mu.Unlock()
	if e.block != nil {
		e.inside.Add(1)
		select {
		case <-e.block:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return e.result, e.err
}

func (e *fakeExecutor) ran() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.jobs)
}

type brokerFixture struct {
	*fixture
	exec    *fakeExecutor
	broker  *Broker
	enabled atomic.Bool
	with    user.User
	without user.User
}

func newBrokerFixture(t *testing.T) *brokerFixture {
	t.Helper()
	f := newFixture(t)
	p := f.profile(t, ProfileInput{
		Name: "Runner", Kind: KindRunner, Enabled: true,
		Languages: []string{"python", "ruby"}, Images: map[string]string{"python": "python:3.12-slim"},
	})
	bf := &brokerFixture{fixture: f, exec: &fakeExecutor{result: Result{Stdout: "42\n"}}}
	bf.broker = NewBroker(f.store, map[string]Executor{KindRunner: bf.exec})
	bf.broker.Enabled = bf.enabled.Load
	bf.enabled.Store(true)
	bf.with = user.User{ID: "u-with", GroupID: f.group(t, "Coders", p.ID).ID}
	bf.without = user.User{ID: "u-without", GroupID: f.group(t, "Readers", "").ID}
	return bf
}

func call(args map[string]any) adapter.ToolCall {
	encoded, _ := json.Marshal(args)
	return adapter.ToolCall{ID: "c1", Name: ToolName, Arguments: string(encoded)}
}

func TestRunCodeIsOfferedOnlyToAGroupWithAProfileWhileTheSwitchIsOn(t *testing.T) {
	f := newBrokerFixture(t)
	ctx := context.Background()

	tools := f.broker.Offer(ctx, f.with)
	if len(tools) != 1 || tools[0].Name != ToolName {
		t.Fatalf("offered %+v", tools)
	}
	// Only the language that is both listed and mapped is in the schema.
	if s := string(tools[0].Parameters); !strings.Contains(s, `"python"`) || strings.Contains(s, `"ruby"`) {
		t.Fatalf("schema = %s", s)
	}
	if got := f.broker.Offer(ctx, f.without); len(got) != 0 {
		t.Fatalf("a group without a profile was offered %+v", got)
	}

	f.enabled.Store(false)
	if got := f.broker.Offer(ctx, f.with); len(got) != 0 {
		t.Fatalf("offered with the switch off: %+v", got)
	}
	if out, failed := f.broker.Run(ctx, f.with, call(map[string]any{"language": "python", "code": "print(1)"})); !failed || f.exec.ran() != 0 {
		t.Fatalf("ran with the switch off: %q", out)
	}
}

func TestRunCodeRunsAndFormatsTheResult(t *testing.T) {
	f := newBrokerFixture(t)
	out, failed := f.broker.Run(context.Background(), f.with,
		call(map[string]any{"language": "Python", "code": "print(6*7)", "stdin": "in"}))
	if failed || !strings.Contains(out, "exit code: 0") || !strings.Contains(out, "42") {
		t.Fatalf("out = %q, failed %v", out, failed)
	}
	if f.exec.ran() != 1 || f.exec.jobs[0].Language != "python" || f.exec.jobs[0].Stdin != "in" || f.exec.jobs[0].UserID != "u-with" {
		t.Fatalf("jobs = %+v", f.exec.jobs)
	}
}

func TestRunCodeRefusesWhatTheProfileCannotRunWithoutRunningIt(t *testing.T) {
	f := newBrokerFixture(t)
	ctx := context.Background()
	cases := []adapter.ToolCall{
		call(map[string]any{"language": "ruby", "code": "p 1"}),
		call(map[string]any{"language": "cobol", "code": "x"}),
		call(map[string]any{"language": "python", "code": "  "}),
		{ID: "c", Name: ToolName, Arguments: "not json"},
		{ID: "c", Name: "user_list", Arguments: "{}"},
	}
	for _, c := range cases {
		if out, failed := f.broker.Run(ctx, f.with, c); !failed {
			t.Errorf("%s %s: not refused: %q", c.Name, c.Arguments, out)
		}
	}
	if out, failed := f.broker.Run(ctx, f.without, call(map[string]any{"language": "python", "code": "1"})); !failed {
		t.Errorf("a group without a profile ran code: %q", out)
	}
	if n := f.exec.ran(); n != 0 {
		t.Fatalf("%d refused calls reached the executor", n)
	}
}

// The profile is read again at Run, so taking it away from a group between
// Offer and Run stops the run.
func TestTakingTheProfileAwayStopsARunThatWasOffered(t *testing.T) {
	f := newBrokerFixture(t)
	ctx := context.Background()
	if len(f.broker.Offer(ctx, f.with)) != 1 {
		t.Fatal("not offered")
	}
	empty := ""
	if _, err := f.groups.Update(ctx, nil, f.with.GroupID, groupUpdate(&empty)); err != nil {
		t.Fatal(err)
	}
	if _, failed := f.broker.Run(ctx, f.with, call(map[string]any{"language": "python", "code": "1"})); !failed || f.exec.ran() != 0 {
		t.Fatal("a run went ahead after the profile was taken away")
	}
}

func TestAFailedProgramIsMarkedFailedAndAnExecutorFailureIsNotBlamedOnTheCode(t *testing.T) {
	f := newBrokerFixture(t)
	ctx := context.Background()
	f.exec.result = Result{ExitCode: 1, Stderr: "Traceback"}
	out, failed := f.broker.Run(ctx, f.with, call(map[string]any{"language": "python", "code": "raise"}))
	if !failed || !strings.Contains(out, "Traceback") {
		t.Fatalf("out = %q, failed %v", out, failed)
	}

	f.exec.err = context.DeadlineExceeded
	out, failed = f.broker.Run(ctx, f.with, call(map[string]any{"language": "python", "code": "1"}))
	if !failed || !strings.Contains(out, "not an error in the code") || strings.Contains(out, "deadline") {
		t.Fatalf("out = %q", out)
	}
}

// The ceiling refuses rather than queues, with real goroutines holding the
// slots.
func TestRunsPastTheCeilingAreRefusedNotQueued(t *testing.T) {
	f := newBrokerFixture(t)
	f.exec.block = make(chan struct{})
	f.broker.MaxConcurrent = func() int { return 2 }
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.broker.Run(ctx, f.with, call(map[string]any{"language": "python", "code": "1"}))
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.exec.inside.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the two runs never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	out, failed := f.broker.Run(ctx, f.with, call(map[string]any{"language": "python", "code": "1"}))
	if !failed || !strings.Contains(out, "busy") {
		t.Fatalf("a third run past a ceiling of two: %q", out)
	}
	close(f.exec.block)
	wg.Wait()
	if _, failed := f.broker.Run(ctx, f.with, call(map[string]any{"language": "python", "code": "1"})); failed {
		t.Fatal("the slots were not given back")
	}
}

func TestTheGuideIsGivenOnlyWhenRunCodeWasOffered(t *testing.T) {
	f := newBrokerFixture(t)
	ctx := context.Background()
	if g := f.broker.Guide(ctx, f.with, f.broker.Offer(ctx, f.with)); !strings.Contains(g, "run_code") {
		t.Fatalf("guide = %q", g)
	}
	if g := f.broker.Guide(ctx, f.without, []adapter.Tool{{Name: "user_list"}}); g != "" {
		t.Fatalf("guide without run_code = %q", g)
	}
}
