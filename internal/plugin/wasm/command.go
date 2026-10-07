package wasm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"testing/fstest"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"
)

// LoadCommand returns a backend for a WASI command module: a program with a
// _start, run once per call with arguments, stdin and a few read-only files,
// whose answer is what it printed. It is how the code sandbox runs an
// interpreter an administrator uploaded.
//
// It has no host calls. The "arc" import module is still on the runtime,
// because build is shared, but a refusal is all it answers: an interpreter
// running a model's code is the last thing that should reach the server.
func (e *Engine) LoadCommand(name string, wasm []byte) *Backend {
	return &Backend{
		name: name, engine: e, wasm: wasm, command: true,
		host: func(*Call, string, json.RawMessage) (any, error) {
			return nil, &HostError{Code: "denied", Message: "a sandboxed program has no host calls"}
		},
	}
}

// RunInput is one run of a command.
type RunInput struct {
	// After argv[0], which is the backend's name.
	Args  []string
	Stdin []byte
	// Mounted read-only at /work, keyed by path inside it. Read-only and in
	// memory: the run can read the code it was given and nothing else, and
	// leaves nothing behind for the next one.
	Files map[string][]byte
	// Per stream. Past it the program keeps running and what it writes is
	// dropped, so a loop that prints forever ends at the timeout rather than
	// in the server's memory.
	MaxOutput int
	// Zero takes the engine's CallTimeout.
	Timeout time.Duration
}

// RunResult is what a run did. A program that crashed or ran out of time is
// still a result, not an error: the model wrote the code and is meant to
// read why it failed.
type RunResult struct {
	Stdout    string        `json:"stdout"`
	Stderr    string        `json:"stderr"`
	ExitCode  int           `json:"exit_code"`
	Truncated bool          `json:"truncated"`
	TimedOut  bool          `json:"timed_out"`
	Crashed   bool          `json:"crashed"`
	Duration  time.Duration `json:"duration"`
}

const defaultMaxOutput = 64 << 10

// Run starts the command on a fresh instance and waits for it to end. The
// only error is one the caller has to act on: the module not compiling, the
// backend removed, or the caller's own context ending.
func (b *Backend) Run(ctx context.Context, in RunInput) (RunResult, error) {
	if !b.command {
		return RunResult{}, errors.New("wasm: Run is for command modules; a plugin backend is called with Invoke")
	}
	w, err := b.acquire(ctx)
	if err != nil {
		return RunResult{}, err
	}
	defer b.release()

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = b.engine.limits.CallTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	limit := in.MaxOutput
	if limit <= 0 {
		limit = defaultMaxOutput
	}
	stdout, stderr := &limitedBuffer{limit: limit}, &limitedBuffer{limit: limit}

	// Real clocks and randomness for the reasons Invoke gives them; sleep
	// too, because an interpreter's time.sleep would otherwise return at once
	// and the program would be wrong about what it did. None of it outlives
	// the deadline: the runtime closes the instance when runCtx ends.
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(append([]string{b.name}, in.Args...)...).
		WithStdin(bytes.NewReader(in.Stdin)).
		WithStdout(stdout).
		WithStderr(stderr).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithRandSource(rand.Reader)
	if len(in.Files) > 0 {
		// fstest.MapFS is the standard library's in-memory fs.FS; it lives
		// in testing/fstest but is an ordinary read-only map, which is exactly
		// what is wanted, and the alternative was writing the same type again.
		files := fstest.MapFS{}
		for name, data := range in.Files {
			files[name] = &fstest.MapFile{Data: data, Mode: 0o444}
		}
		cfg = cfg.WithFSConfig(wazero.NewFSConfig().WithFSMount(files, "/work"))
	}

	start := time.Now()
	mod, runErr := w.rt.InstantiateModule(runCtx, w.compiled, cfg)
	if mod != nil {
		_ = mod.Close(context.Background())
	}
	result := RunResult{Duration: time.Since(start)}
	finish := func() (RunResult, error) {
		result.Stdout, result.Stderr = stdout.String(), stderr.String()
		result.Truncated = stdout.Cut() || stderr.Cut()
		return result, nil
	}

	if runErr == nil {
		return finish()
	}
	// The caller going away is the caller's answer, not the program's.
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut, result.ExitCode = true, -1
		return finish()
	}
	var exit *sys.ExitError
	if errors.As(runErr, &exit) {
		result.ExitCode = int(exit.ExitCode())
		return finish()
	}
	// A trap: memory past the ceiling, an interpreter bug. Said on stderr,
	// where the model looks for why its program stopped.
	result.Crashed, result.ExitCode = true, -1
	_, _ = stderr.Write([]byte("\n[sandbox] the program crashed: " + runErr.Error()))
	return finish()
}

// limitedBuffer keeps the first limit bytes written to it and drops the
// rest, reporting success either way: an interpreter that saw its stdout
// fail would stop with an error of its own, and the cut is reported once,
// in RunResult.Truncated, instead.
type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
	cut   bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.limit - l.buf.Len(); room < len(p) {
		if room > 0 {
			l.buf.Write(p[:room])
		}
		l.cut = true
		return len(p), nil
	}
	l.buf.Write(p)
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *limitedBuffer) Cut() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cut
}
