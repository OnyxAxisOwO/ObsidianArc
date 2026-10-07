package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// Job is one piece of code to run.
type Job struct {
	UserID   string
	Language string
	Code     string
	Stdin    string
}

// Result is what a run did, whichever backend ran it. A program that failed
// is a result; an error is reserved for "it could not be run at all".
type Result struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated"`
	TimedOut   bool   `json:"timed_out"`
	Crashed    bool   `json:"crashed"`
	DurationMS int64  `json:"duration_ms"`
}

// Executor runs a job under a profile.
type Executor interface {
	Run(ctx context.Context, profile Profile, job Job) (Result, error)
}

// ErrUnavailable is a language the profile cannot run right now: not listed,
// mapped to nothing, or mapped to an interpreter that was deleted. The model
// is told so and can choose another language.
var ErrUnavailable = errors.New("sandbox: that language is not available here")

// fileNames is what the code is called inside /work. Some interpreters read
// the extension (QuickJS treats .mjs as a module), so the common ones get the
// one they expect; anything else gets a name that claims nothing.
var fileNames = map[string]string{
	"javascript": "main.js",
	"js":         "main.js",
	"python":     "main.py",
	"ruby":       "main.rb",
	"lua":        "main.lua",
}

func fileNameFor(language string) string {
	if name, ok := fileNames[language]; ok {
		return name
	}
	return "main.code"
}

// WasmExecutor runs code inside the server, on an interpreter an
// administrator uploaded, through the plugin runtime's command entry.
//
// It keeps a backend per interpreter digest and memory ceiling. The backend
// gives its compiled code back after wasm.Limits.IdleEvict like a plugin's
// does, so an instance that runs code now and then does not keep an
// interpreter's tens of megabytes of native code resident between runs.
type WasmExecutor struct {
	store    *Store
	cacheDir string

	mu       sync.Mutex
	backends map[string]*cachedBackend
}

type cachedBackend struct {
	interpreterID string
	backend       *wasm.Backend
}

func NewWasmExecutor(store *Store, cacheDir string) *WasmExecutor {
	return &WasmExecutor{store: store, cacheDir: cacheDir, backends: map[string]*cachedBackend{}}
}

func (e *WasmExecutor) Run(ctx context.Context, profile Profile, job Job) (Result, error) {
	if profile.Kind != KindWasm || !profile.Runs(job.Language) {
		return Result{}, ErrUnavailable
	}
	interp, err := e.store.Interpreter(ctx, profile.Images[job.Language])
	if errors.Is(err, ErrNotFound) {
		return Result{}, ErrUnavailable
	}
	if err != nil {
		return Result{}, err
	}
	backend, err := e.backend(ctx, interp, profile.MemoryMB)
	if err != nil {
		return Result{}, err
	}

	file := fileNameFor(job.Language)
	in := wasm.RunInput{
		MaxOutput: int(profile.MaxOutputBytes),
		Timeout:   time.Duration(profile.TimeoutMS) * time.Millisecond,
	}
	usesFile := false
	for _, arg := range interp.Args {
		if strings.Contains(arg, "{file}") {
			usesFile = true
		}
		in.Args = append(in.Args, strings.ReplaceAll(arg, "{file}", "/work/"+file))
	}
	if usesFile {
		in.Files = map[string][]byte{file: []byte(job.Code)}
		in.Stdin = []byte(job.Stdin)
	} else {
		// An interpreter that reads its program from stdin has no stdin left
		// for the program; the tool's description says stdin needs an
		// interpreter configured with {file}.
		in.Stdin = []byte(job.Code)
	}

	ran, err := backend.Run(ctx, in)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Stdout: ran.Stdout, Stderr: ran.Stderr, ExitCode: ran.ExitCode,
		Truncated: ran.Truncated, TimedOut: ran.TimedOut, Crashed: ran.Crashed,
		DurationMS: ran.Duration.Milliseconds(),
	}, nil
}

// backend returns the loaded backend for this interpreter's current bytes at
// this memory ceiling. A re-upload under the same id has a new digest, so it
// gets a new backend and the old one is closed, giving its code back.
func (e *WasmExecutor) backend(ctx context.Context, interp Interpreter, memoryMB int64) (*wasm.Backend, error) {
	key := fmt.Sprintf("%s:%s:%d", interp.ID, interp.SHA256, memoryMB)
	e.mu.Lock()
	if cached, ok := e.backends[key]; ok {
		e.mu.Unlock()
		return cached.backend, nil
	}
	e.mu.Unlock()

	// Read outside the lock: a module is megabytes, and another run of an
	// interpreter that is already loaded should not wait on it.
	module, err := e.store.Module(ctx, interp.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, err
	}
	engine := wasm.NewEngine(wasm.Limits{
		// 16 pages of 64 KiB to the megabyte.
		MemoryPages: uint32(memoryMB * 16),
		CacheDir:    e.cacheDir,
	})
	fresh := engine.LoadCommand(interp.Language, module)

	e.mu.Lock()
	if cached, ok := e.backends[key]; ok {
		// Somebody else loaded it while we read; theirs wins and ours is
		// dropped before it ever compiled.
		e.mu.Unlock()
		fresh.Close()
		return cached.backend, nil
	}
	var stale []*wasm.Backend
	for k, cached := range e.backends {
		if cached.interpreterID == interp.ID && !strings.HasPrefix(k, interp.ID+":"+interp.SHA256+":") {
			stale = append(stale, cached.backend)
			delete(e.backends, k)
		}
	}
	e.backends[key] = &cachedBackend{interpreterID: interp.ID, backend: fresh}
	e.mu.Unlock()

	// Close lets calls already inside finish, so a run on the old bytes is
	// not cut off by an upload of new ones.
	for _, b := range stale {
		b.Close()
	}
	return fresh, nil
}

// Forget closes every backend of an interpreter, for a delete: what it
// compiled is given back now rather than at the next idle eviction.
func (e *WasmExecutor) Forget(interpreterID string) {
	e.mu.Lock()
	var gone []*wasm.Backend
	for k, cached := range e.backends {
		if cached.interpreterID == interpreterID {
			gone = append(gone, cached.backend)
			delete(e.backends, k)
		}
	}
	e.mu.Unlock()
	for _, b := range gone {
		b.Close()
	}
}

// Close gives every backend back, at shutdown.
func (e *WasmExecutor) Close() {
	e.mu.Lock()
	all := e.backends
	e.backends = map[string]*cachedBackend{}
	e.mu.Unlock()
	for _, cached := range all {
		cached.backend.Close()
	}
}
