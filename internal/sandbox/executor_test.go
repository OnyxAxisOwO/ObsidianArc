package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var (
	standInOnce sync.Once
	standInWasm []byte
	standInErr  error
)

// standIn builds the plugin runtime's test command, which plays an
// interpreter: told "file PATH" it prints the file, told "echo" it prints
// stdin. That is enough to prove the code reaches the program the way the
// interpreter's argv says it should.
func standIn(t *testing.T) []byte {
	t.Helper()
	standInOnce.Do(func() {
		dir, err := os.MkdirTemp("", "arcsandbox")
		if err != nil {
			standInErr = err
			return
		}
		out := filepath.Join(dir, "standin.wasm")
		cmd := exec.Command("go", "build", "-o", out, "../plugin/wasm/testdata/command")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if b, err := cmd.CombinedOutput(); err != nil {
			standInErr = errors.New(string(b))
			return
		}
		standInWasm, standInErr = os.ReadFile(out)
	})
	if standInErr != nil {
		t.Fatalf("building the stand-in interpreter: %v", standInErr)
	}
	return standInWasm
}

func (f *fixture) interpreter(t *testing.T, name string, args []string) Interpreter {
	t.Helper()
	interp, err := f.store.CreateInterpreter(context.Background(), InterpreterInput{
		Name: name, Language: "javascript", Args: args, Module: standIn(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return interp
}

func newExecutor(t *testing.T, f *fixture) *WasmExecutor {
	t.Helper()
	e := NewWasmExecutor(f.store, "")
	t.Cleanup(e.Close)
	return e
}

func TestTheCodeReachesAnInterpreterAsAFileWhenItsArgvSaysSo(t *testing.T) {
	f := newFixture(t)
	interp := f.interpreter(t, "FileJS", []string{"file", "{file}"})
	p := f.profile(t, ProfileInput{
		Name: "Light", Kind: KindWasm, Enabled: true,
		Languages: []string{"javascript"}, Images: map[string]string{"javascript": interp.ID},
	})
	got, err := newExecutor(t, f).Run(t.Context(), p, Job{Language: "javascript", Code: "console.log(1)"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Stdout != "console.log(1)" || got.ExitCode != 0 {
		t.Fatalf("result = %+v", got)
	}
}

func TestTheCodeReachesAnInterpreterOnStdinOtherwise(t *testing.T) {
	f := newFixture(t)
	interp := f.interpreter(t, "StdinJS", []string{"echo"})
	p := f.profile(t, ProfileInput{
		Name: "Light", Kind: KindWasm, Enabled: true,
		Languages: []string{"javascript"}, Images: map[string]string{"javascript": interp.ID},
	})
	got, err := newExecutor(t, f).Run(t.Context(), p, Job{Language: "javascript", Code: "1+1"})
	if err != nil || got.Stdout != "1+1" {
		t.Fatalf("result = %+v, %v", got, err)
	}
}

func TestTheProfilesLimitsBindTheRun(t *testing.T) {
	f := newFixture(t)
	spin := f.interpreter(t, "Spin", []string{"spin"})
	p := f.profile(t, ProfileInput{
		Name: "Short", Kind: KindWasm, Enabled: true, TimeoutMS: 300,
		Languages: []string{"javascript"}, Images: map[string]string{"javascript": spin.ID},
	})
	start := time.Now()
	got, err := newExecutor(t, f).Run(t.Context(), p, Job{Language: "javascript", Code: "while(1){}"})
	if err != nil || !got.TimedOut {
		t.Fatalf("result = %+v, %v", got, err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a 300ms timeout ran for %s", took)
	}

	flood := f.interpreter(t, "Flood", []string{"flood"})
	p2 := f.profile(t, ProfileInput{
		Name: "Small", Kind: KindWasm, Enabled: true, MaxOutputBytes: 2048,
		Languages: []string{"javascript"}, Images: map[string]string{"javascript": flood.ID},
	})
	got, err = newExecutor(t, f).Run(t.Context(), p2, Job{Language: "javascript"})
	if err != nil || len(got.Stdout) != 2048 || !got.Truncated {
		t.Fatalf("flood: %d bytes, truncated %v, %v", len(got.Stdout), got.Truncated, err)
	}
}

// Every way a language can be missing is the same answer, and none of them
// runs anything.
func TestALanguageThatMapsToNothingIsUnavailable(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	interp := f.interpreter(t, "JS", []string{"echo"})
	p := f.profile(t, ProfileInput{
		Name: "Light", Kind: KindWasm, Enabled: true,
		Languages: []string{"javascript", "python"}, Images: map[string]string{"javascript": interp.ID},
	})
	e := newExecutor(t, f)

	if _, err := e.Run(ctx, p, Job{Language: "python"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("listed but unmapped: %v", err)
	}
	if _, err := e.Run(ctx, p, Job{Language: "ruby"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("not listed: %v", err)
	}
	runner := p
	runner.Kind = KindRunner
	if _, err := e.Run(ctx, runner, Job{Language: "javascript"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a runner profile on the wasm executor: %v", err)
	}
	disabled := p
	disabled.Enabled = false
	if _, err := e.Run(ctx, disabled, Job{Language: "javascript"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a disabled profile: %v", err)
	}

	if _, err := e.Run(ctx, p, Job{Language: "javascript", Code: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteInterpreter(ctx, interp.ID); err != nil {
		t.Fatal(err)
	}
	e.Forget(interp.ID)
	if _, err := e.Run(ctx, p, Job{Language: "javascript"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a deleted interpreter: %v", err)
	}
}

// Many runs of one interpreter at once, with real goroutines, share one
// backend and each get their own answer.
func TestParallelRunsOfOneInterpreterEachGetTheirOwnAnswer(t *testing.T) {
	f := newFixture(t)
	interp := f.interpreter(t, "JS", []string{"echo"})
	p := f.profile(t, ProfileInput{
		Name: "Light", Kind: KindWasm, Enabled: true,
		Languages: []string{"javascript"}, Images: map[string]string{"javascript": interp.ID},
	})
	e := newExecutor(t, f)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := fmt.Sprintf("run-%d", i)
			got, err := e.Run(context.Background(), p, Job{Language: "javascript", Code: want})
			if err != nil || got.Stdout != want {
				errs <- fmt.Errorf("run %d: %+v, %v", i, got, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	e.mu.Lock()
	n := len(e.backends)
	e.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d backends for one interpreter", n)
	}
}
