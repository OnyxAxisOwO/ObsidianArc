package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/sandbox"
)

// The test binary doubles as a fake docker command: with FAKE_DOCKER set it
// behaves as `docker run` / `docker kill` would for the cases the runner has
// to tell apart, so the tests run on a machine with no Docker at all.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_DOCKER"); mode != "" {
		os.Exit(fakeDocker(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeDocker(mode string, args []string) int {
	if len(args) == 0 {
		return 2
	}
	if args[0] == "kill" {
		if log := os.Getenv("FAKE_DOCKER_LOG"); log != "" {
			_ = os.WriteFile(log, []byte(strings.Join(args, " ")), 0o644)
		}
		return 0
	}
	if args[0] != "run" {
		return 2
	}
	if log := os.Getenv("FAKE_DOCKER_ARGS"); log != "" {
		_ = os.WriteFile(log, []byte(strings.Join(args, "\n")), 0o644)
	}
	var dir string
	for i, a := range args {
		if a == "-v" && i+1 < len(args) {
			dir = strings.TrimSuffix(args[i+1], ":/work:ro")
		}
	}
	switch mode {
	case "echo":
		file := filepath.Join(dir, filepath.Base(args[len(args)-1]))
		code, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Print("ran:" + string(code))
		in, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(os.Stderr, "stdin:"+string(in))
		return 0
	case "exit3":
		fmt.Fprint(os.Stderr, "boom")
		return 3
	case "noimage":
		fmt.Fprint(os.Stderr, "Unable to find image")
		return 125
	case "oom":
		return 137
	case "flood":
		chunk := strings.Repeat("x", 4096)
		for i := 0; i < 256; i++ {
			fmt.Print(chunk)
			fmt.Fprint(os.Stderr, chunk)
		}
		return 0
	case "sleep":
		time.Sleep(time.Second)
		return 0
	}
	return 2
}

func fakeDockerCLI(t *testing.T, mode string) *dockerCLI {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DOCKER", mode)
	return &dockerCLI{bin: bin, workRoot: t.TempDir()}
}

func testJob() job {
	return job{ID: "j1", Language: "python", Image: "python:3.12-slim", Code: "print(1)",
		Stdin: "hi", TimeoutMS: 5000, MemoryMB: 64, MaxOutputBytes: 1 << 16, LeaseMS: 300}
}

// The wire types are repeated here rather than imported, so this is what
// keeps them the server's: the same JSON names, field for field.
func TestTheWireTypesMatchTheServers(t *testing.T) {
	names := func(v any) []string {
		var out []string
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			out = append(out, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
		}
		slices.Sort(out)
		return out
	}
	if a, b := names(job{}), names(sandbox.ClaimedJob{}); !slices.Equal(a, b) {
		t.Errorf("job %v, server %v", a, b)
	}
	if a, b := names(result{}), names(sandbox.Result{}); !slices.Equal(a, b) {
		t.Errorf("result %v, server %v", a, b)
	}
}

func TestTheContainerIsLockedDown(t *testing.T) {
	args := strings.Join(dockerArgs(testJob(), "arc-job-j1", "/tmp/d", commands["python"]), " ")
	for _, want := range []string{
		"--network none", "--read-only", "--cap-drop ALL", "no-new-privileges",
		"--user 65534:65534", "--pids-limit", "--memory 64m --memory-swap 64m",
		"-v /tmp/d:/work:ro", "python:3.12-slim python /work/main.py",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("docker args lack %q: %s", want, args)
		}
	}
}

func TestARunReportsWhatTheProgramPrinted(t *testing.T) {
	d := fakeDockerCLI(t, "echo")
	r, err := d.Execute(context.Background(), testJob())
	if err != nil || r.Stdout != "ran:print(1)" || r.Stderr != "stdin:hi" || r.ExitCode != 0 || r.TimedOut {
		t.Fatalf("result = %+v, %v", r, err)
	}
	if entries, _ := os.ReadDir(d.workRoot); len(entries) != 0 {
		t.Fatalf("the job's directory was left behind: %v", entries)
	}
}

func TestAFailingProgramIsAResultAndAFailingDockerIsNot(t *testing.T) {
	r, err := fakeDockerCLI(t, "exit3").Execute(context.Background(), testJob())
	if err != nil || r.ExitCode != 3 || r.Stderr != "boom" {
		t.Fatalf("exit 3 = %+v, %v", r, err)
	}
	if _, err := fakeDockerCLI(t, "noimage").Execute(context.Background(), testJob()); err == nil {
		t.Fatal("docker failing to start a container was reported as the program's result")
	}
	r, err = fakeDockerCLI(t, "oom").Execute(context.Background(), testJob())
	if err != nil || !r.Crashed || r.TimedOut {
		t.Fatalf("a kernel kill = %+v, %v", r, err)
	}
	j := testJob()
	j.Language = "cobol"
	if _, err := fakeDockerCLI(t, "echo").Execute(context.Background(), j); err == nil {
		t.Fatal("a language the runner cannot start ran")
	}
}

func TestOutputSharesOneCeiling(t *testing.T) {
	j := testJob()
	j.MaxOutputBytes = 10000
	r, err := fakeDockerCLI(t, "flood").Execute(context.Background(), j)
	if err != nil || !r.Truncated || len(r.Stdout)+len(r.Stderr) != 10000 {
		t.Fatalf("flood = %d+%d bytes, truncated %v, %v", len(r.Stdout), len(r.Stderr), r.Truncated, err)
	}
}

// Past its time limit the container itself is killed, by name: killing the
// docker command alone would leave the program running.
func TestATimedOutRunKillsTheContainer(t *testing.T) {
	d := fakeDockerCLI(t, "sleep")
	log := filepath.Join(t.TempDir(), "kill")
	t.Setenv("FAKE_DOCKER_LOG", log)
	j := testJob()
	j.TimeoutMS = 100
	r, err := d.Execute(context.Background(), j)
	if err != nil || !r.TimedOut {
		t.Fatalf("result = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(log); string(got) != "kill arc-job-j1" {
		t.Fatalf("kill was %q", got)
	}
}

// fakeServer speaks the runner protocol from the server's side.
type fakeServer struct {
	mu        sync.Mutex
	queue     []job
	reports   map[string]map[string]any
	cancel    atomic.Bool
	beats     atomic.Int32
	badTokens atomic.Int32
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer sk-oa-runner-good" {
				f.badTokens.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/sandbox/runner/claim", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.queue) == 0 {
			time.Sleep(20 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		j := f.queue[0]
		f.queue = f.queue[1:]
		_ = json.NewEncoder(w).Encode(map[string]any{"job": j})
	}))
	mux.HandleFunc("POST /api/sandbox/runner/jobs/{id}/heartbeat", auth(func(w http.ResponseWriter, r *http.Request) {
		f.beats.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"cancel": f.cancel.Load()})
	}))
	mux.HandleFunc("POST /api/sandbox/runner/jobs/{id}/result", auth(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.reports[r.PathValue("id")] = body
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	return mux
}

func (f *fakeServer) report(id string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reports[id]
	return r, ok
}

// stubExecutor stands in for Docker in the protocol tests.
type stubExecutor struct {
	delay time.Duration
}

func (s stubExecutor) Execute(ctx context.Context, j job) (result, error) {
	select {
	case <-ctx.Done():
		return result{}, ctx.Err()
	case <-time.After(s.delay):
	}
	return result{Stdout: "ran " + j.Code}, nil
}

func TestTheRunnerClaimsRunsAndReports(t *testing.T) {
	f := &fakeServer{reports: map[string]map[string]any{}}
	f.queue = []job{{ID: "a", Language: "python", Code: "1", LeaseMS: 90}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- work(ctx, newClient(srv.URL, "sk-oa-runner-good"), stubExecutor{delay: 100 * time.Millisecond})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if r, ok := f.report("a"); ok {
			res, _ := r["result"].(map[string]any)
			if res["stdout"] != "ran 1" || r["failed"] != false {
				t.Fatalf("report = %v", r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no report")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.beats.Load() == 0 {
		t.Error("a run longer than a third of its lease sent no heartbeat")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("work ended with %v", err)
	}
}

// A heartbeat that says the requester has gone stops the run, and nothing
// is reported for it.
func TestACancelOnHeartbeatStopsTheRunAndReportsNothing(t *testing.T) {
	f := &fakeServer{reports: map[string]map[string]any{}}
	f.cancel.Store(true)
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	started := time.Now()
	handle(context.Background(), newClient(srv.URL, "sk-oa-runner-good"),
		stubExecutor{delay: 5 * time.Second}, job{ID: "b", Code: "1", LeaseMS: 60})
	if time.Since(started) > 2*time.Second {
		t.Fatal("a cancelled run was not stopped")
	}
	if _, ok := f.report("b"); ok {
		t.Fatal("a cancelled run was reported")
	}
}

func TestARefusedTokenEndsTheRunner(t *testing.T) {
	f := &fakeServer{reports: map[string]map[string]any{}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	err := work(context.Background(), newClient(srv.URL, "sk-oa-runner-bad"), stubExecutor{})
	if err != errUnauthorized || f.badTokens.Load() != 1 {
		t.Fatalf("work = %v after %d refusals", err, f.badTokens.Load())
	}
}
