package wasm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	commandOnce sync.Once
	commandWasm []byte
	commandErr  error
)

// command builds testdata/command once for the whole run: an ordinary
// wasip1 executable, which is what an uploaded interpreter is too.
func command(t *testing.T) []byte {
	t.Helper()
	commandOnce.Do(func() {
		dir, err := os.MkdirTemp("", "arccommand")
		if err != nil {
			commandErr = err
			return
		}
		out := filepath.Join(dir, "command.wasm")
		cmd := exec.Command("go", "build", "-o", out, "./testdata/command")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if b, err := cmd.CombinedOutput(); err != nil {
			commandErr = errors.New(string(b))
			return
		}
		commandWasm, commandErr = os.ReadFile(out)
	})
	if commandErr != nil {
		t.Fatalf("building the test command: %v", commandErr)
	}
	return commandWasm
}

func loadCommand(t *testing.T, limits Limits) *Backend {
	t.Helper()
	b := NewEngine(limits).LoadCommand("cmd", command(t))
	t.Cleanup(b.Close)
	if err := b.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func run(t *testing.T, b *Backend, in RunInput) RunResult {
	t.Helper()
	result, err := b.Run(t.Context(), in)
	if err != nil {
		t.Fatalf("run %v: %v", in.Args, err)
	}
	return result
}

func TestACommandReadsStdinAndArgsAndPrints(t *testing.T) {
	b := loadCommand(t, Limits{})
	if got := run(t, b, RunInput{Args: []string{"echo"}, Stdin: []byte("hello")}); got.Stdout != "hello" || got.ExitCode != 0 {
		t.Fatalf("echo = %+v", got)
	}
	if got := run(t, b, RunInput{Args: []string{"args", "a", "b c"}}); got.Stdout != "a,b c" {
		t.Fatalf("args = %+v", got)
	}
}

func TestACommandsExitCodeAndStderrAreItsResultNotAnError(t *testing.T) {
	b := loadCommand(t, Limits{})
	got := run(t, b, RunInput{Args: []string{"exit", "3"}})
	if got.ExitCode != 3 || got.Stderr != "leaving" || got.Crashed || got.TimedOut {
		t.Fatalf("exit = %+v", got)
	}
}

func TestTheFilesGivenAreReadableAndNothingIsWritable(t *testing.T) {
	b := loadCommand(t, Limits{})
	files := map[string][]byte{"main.js": []byte("print(1)")}
	if got := run(t, b, RunInput{Args: []string{"file", "/work/main.js"}, Files: files}); got.Stdout != "print(1)" {
		t.Fatalf("file = %+v", got)
	}
	if got := run(t, b, RunInput{Args: []string{"write", "/work/out.txt"}, Files: files}); got.Stdout != "denied" {
		t.Fatalf("the program wrote into its mount: %+v", got)
	}
}

func TestACommandSeesNoHostFilesystemAndNoEnvironment(t *testing.T) {
	b := loadCommand(t, Limits{})
	if got := run(t, b, RunInput{Args: []string{"hostfs"}}); got.Stdout != "denied" {
		t.Fatalf("the program listed the host's filesystem: %q", got.Stdout)
	}
	if got := run(t, b, RunInput{Args: []string{"env"}}); got.Stdout != "0" {
		t.Fatalf("the program saw %s environment variables", got.Stdout)
	}
}

func TestOutputPastTheLimitIsCutAndSaidToBe(t *testing.T) {
	b := loadCommand(t, Limits{})
	got := run(t, b, RunInput{Args: []string{"flood"}, MaxOutput: 10 << 10})
	if len(got.Stdout) != 10<<10 || !got.Truncated || got.ExitCode != 0 {
		t.Fatalf("flood: %d bytes, truncated %v, exit %d", len(got.Stdout), got.Truncated, got.ExitCode)
	}
}

func TestAProgramThatNeverEndsIsCutOffAndTheBackendCarriesOn(t *testing.T) {
	b := loadCommand(t, Limits{})
	start := time.Now()
	got := run(t, b, RunInput{Args: []string{"spin"}, Timeout: 300 * time.Millisecond})
	if !got.TimedOut {
		t.Fatalf("spin = %+v", got)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the spin ran for %s", took)
	}
	if got := run(t, b, RunInput{Args: []string{"echo"}, Stdin: []byte("ok")}); got.Stdout != "ok" {
		t.Fatalf("after a timeout: %+v", got)
	}
}

func TestTheCallersContextEndingIsAnErrorNotAResult(t *testing.T) {
	b := loadCommand(t, Limits{})
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := b.Run(ctx, RunInput{Args: []string{"spin"}, Timeout: 10 * time.Second}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestSleepReallySleeps(t *testing.T) {
	b := loadCommand(t, Limits{})
	got := run(t, b, RunInput{Args: []string{"sleep", "100"}})
	ms, err := strconv.Atoi(got.Stdout)
	if err != nil || ms < 90 {
		t.Fatalf("sleep(100) took %q ms", got.Stdout)
	}
}

func TestMemoryPastTheCeilingIsACrashReportedOnStderr(t *testing.T) {
	b := loadCommand(t, Limits{MemoryPages: 512}) // 32 MiB; the program asks for 300
	got := run(t, b, RunInput{Args: []string{"memhog"}})
	if got.ExitCode == 0 || got.Stdout == "300" {
		t.Fatalf("a program allocated past its ceiling: %+v", got)
	}
}

func TestAModuleWithoutStartIsNotACommand(t *testing.T) {
	empty := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	b := NewEngine(Limits{}).LoadCommand("bare", empty)
	defer b.Close()
	if err := b.Ready(t.Context()); err == nil || !strings.Contains(err.Error(), "_start") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAndInvokeRefuseTheOtherKindOfModule(t *testing.T) {
	b := load(t, Limits{}, nil)
	if _, err := b.Run(t.Context(), RunInput{}); err == nil {
		t.Fatal("a plugin backend was run as a command")
	}
}

// Runs, compiles and evictions racing each other, with real goroutines:
// every run has to get its own answer whichever state it lands in.
func TestRunsAcrossEvictionsAllSucceed(t *testing.T) {
	b := loadCommand(t, Limits{IdleEvict: 15 * time.Millisecond})
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				time.Sleep(time.Duration((g*7+i*11)%30) * time.Millisecond)
				want := fmt.Sprintf("%d-%d", g, i)
				got, err := b.Run(t.Context(), RunInput{Args: []string{"echo"}, Stdin: []byte(want)})
				if err != nil || got.Stdout != want {
					errs <- fmt.Errorf("goroutine %d run %d: %+v, %v", g, i, got, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
