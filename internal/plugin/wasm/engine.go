// Package wasm runs a plugin's backend: a WebAssembly module, sandboxed by
// wazero, that the server calls with a JSON request and that calls back into
// the server, through one import, for everything it is allowed to touch.
//
// The module is a Go program built for wasip1 as a reactor, so the plugin is
// written in the language the rest of this project is. It sees no
// filesystem, no network, no environment and no clock it can set (it reads the
// host's, which is what time.Now() must answer): the only
// way out is the import below, and what that reaches is decided by the
// HostFunc the server hands in and the permissions the operator granted.
//
// A call gets a module instance of its own and throws it away. Instantiating
// costs a couple of milliseconds — the compiled code is shared — and what it
// buys is that no call can see another's memory, a plugin that leaks or traps
// leaves nothing behind, and there is no pool to size, warm or drain. The
// calls this serves (a sign-up check, a backoffice endpoint, a console
// command) are not on any hot path.
//
// The ABI, which the SDK's guest half implements (sdk/arc):
//
//	exports  _initialize()                 the Go runtime's reactor entry
//	         arc_alloc(n) ptr              n bytes of guest memory, for a request
//	         arc_call(ptr, n) ptr<<32|len  one request in, one response out
//	imports  arc.host(req, n, buf, cap)    a host call; the response is written
//	                                       to buf if it fits, else its size is
//	                                       returned negated and arc.host_read
//	                                       fetches it into a bigger buffer
//	         arc.host_read(buf, cap)       the response the last host call left
//
// Requests and responses are the JSON envelopes below. Two functions in and
// out of guest memory, a buffer the guest owns: nothing here needs the host
// to call back into the guest while the guest is calling the host, which is
// the one thing wasm runtimes disagree about.
package wasm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Limits bound what one call may cost.
type Limits struct {
	// Guest memory, in 64 KiB pages. A Go module needs a few megabytes to
	// start; the rest is what a plugin may use before it traps.
	MemoryPages uint32
	// How long a call may run, unless its context ends sooner.
	CallTimeout time.Duration
	// The largest request or response either side will read.
	MaxMessage int
	// How long a backend may go unused before its compiled code is given back.
	// The next call compiles it again. Negative means never.
	IdleEvict time.Duration
	// A directory to keep compiled code in, so that compiling again — after an
	// eviction, after a restart — is reading a file instead of the seventeen
	// seconds a 5 MB module takes an ordinary server core. Empty keeps none.
	// Nothing in it is anything but a cache: it can be deleted at any time.
	CacheDir string
}

// DefaultLimits are 64 MiB, 15 seconds, 8 MiB, and three minutes of idleness.
func DefaultLimits() Limits {
	return Limits{MemoryPages: 1024, CallTimeout: 15 * time.Second, MaxMessage: 8 << 20, IdleEvict: 3 * time.Minute}
}

// Engine compiles backends.
type Engine struct {
	limits Limits
}

// shared is the compiled code kept across backends, keyed by the module's
// bytes, once ShareCompiledCode has been called.
var shared atomic.Pointer[wazero.CompilationCache]

// ShareCompiledCode makes every backend compiled from here on draw on one
// cache, so a process that builds many servers around one plugin compiles it
// once: seconds saved per test. A server does not call it. Such a cache cannot
// drop an entry, so a plugin that was updated or removed would leave its old
// native code in memory — tens of megabytes each — until the process ended;
// without it, closing a backend gives the code back.
func ShareCompiledCode() {
	shared.CompareAndSwap(nil, ptrTo(wazero.NewCompilationCache()))
}

func ptrTo[T any](v T) *T { return &v }

// NewEngine returns an engine with limits; a zero field takes its default.
func NewEngine(limits Limits) *Engine {
	d := DefaultLimits()
	if limits.MemoryPages == 0 {
		limits.MemoryPages = d.MemoryPages
	}
	if limits.CallTimeout == 0 {
		limits.CallTimeout = d.CallTimeout
	}
	if limits.MaxMessage == 0 {
		limits.MaxMessage = d.MaxMessage
	}
	if limits.IdleEvict == 0 {
		limits.IdleEvict = d.IdleEvict
	}
	return &Engine{limits: limits}
}

// Actor is the account a call is made on behalf of, when there is one.
type Actor struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions,omitempty"`
}

// CallInfo is what a guest is told about the call it is serving.
type CallInfo struct {
	Plugin    string `json:"plugin"`
	Lang      string `json:"lang,omitempty"`
	IP        string `json:"ip,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Actor     *Actor `json:"actor,omitempty"`
}

// Call is one invocation, as the host function sees it.
type Call struct {
	// The invocation's context: cancelled when the request that caused it
	// goes away, or when the call's time runs out.
	Ctx  context.Context
	Info CallInfo
	// Owned by whoever started the invocation, for whatever a host function
	// needs to keep between the calls of one invocation — an open
	// transaction, say. The engine never looks at it.
	State any

	pending []byte
	stderr  cappedBuffer
}

// HostFunc answers one host call. The result is JSON-encoded into the
// response; an error is reported to the guest as one, with its code if it is
// a *HostError.
type HostFunc func(c *Call, op string, arg json.RawMessage) (any, error)

// HostError is an error a guest is told about with a code of its own.
type HostError struct {
	Code    string
	Message string
}

func (e *HostError) Error() string { return e.Message }

// GuestError is a guest's own answer that it could not do what it was asked.
// Status is the HTTP status it wants a client to see, when the call was one
// and it has an opinion; Details are the extra fields of the error body.
type GuestError struct {
	Code    string
	Message string
	Status  int
	Details map[string]any
	// The guest says the failure is one nobody chose — a panic, an error it
	// did not word for a client — and that its message is for the log.
	Internal bool
}

func (e *GuestError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// ErrTrap is a guest that crashed: a panic, an out-of-bounds access, memory
// past its limit. What it printed on the way down is in the message.
var ErrTrap = errors.New("wasm: the plugin's backend crashed")

// Backend is one plugin's backend: its WebAssembly, and — while it is in use —
// the native code compiled from it.
//
// The code is the cost. A Go module is around five megabytes of WebAssembly
// and tens of megabytes once compiled, resident for as long as it is kept, and
// what the backends of a site's plugins serve is mostly things somebody does
// now and then: an operator on a backoffice page, a sign-up. So the compile is
// done when a call needs it and given back when the backend has been idle for
// a while (Limits.IdleEvict); the next call pays for it again, which is under a
// second. A backend that is never called costs the bytes it was loaded from.
type Backend struct {
	name   string
	engine *Engine
	host   HostFunc
	wasm   []byte

	mu        sync.Mutex
	warm      *warm         // the compiled code, nil while there is none
	compiling chan struct{} // closed when the compile in progress is over
	err       error         // a module that does not compile does not start to
	users     int           // calls inside warm, which is not taken from under them
	idle      *time.Timer
	closed    bool
}

// warm is what a compile produces: a runtime with the host's imports and the
// module compiled on it.
type warm struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	// This warm state's own handle on the cache directory. The runtime keeps
	// what it compiled in memory for as long as the handle lives, so closing
	// the handle with the runtime is what gives the code back; the files stay.
	cache wazero.CompilationCache
}

var errRemoved = errors.New("wasm: the backend has been removed")

// Load returns a backend for wasm. It compiles nothing yet: Ready does, for an
// installer that wants to know the module is sound before it commits to
// anything, and otherwise the first call does.
func (e *Engine) Load(name string, wasm []byte, host HostFunc) *Backend {
	return &Backend{name: name, engine: e, host: host, wasm: wasm}
}

// build compiles the module. It holds no lock: it takes most of a second.
func (b *Backend) build() (*warm, error) {
	ctx := context.Background()
	// The host's real clocks (read-only) and real randomness are given to each
	// call's instance below; what is configured here is the runtime.
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(b.engine.limits.MemoryPages)
	var dirCache wazero.CompilationCache
	if cache := shared.Load(); cache != nil {
		cfg = cfg.WithCompilationCache(*cache)
	} else if dir := b.engine.limits.CacheDir; dir != "" {
		if c, err := wazero.NewCompilationCacheWithDir(dir); err != nil {
			slog.Warn("plugin compile cache unavailable; compiling without it", "dir", dir, "error", err)
		} else {
			dirCache = c
			cfg = cfg.WithCompilationCache(c)
		}
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	fail := func(err error) (*warm, error) {
		_ = rt.Close(ctx)
		if dirCache != nil {
			_ = dirCache.Close(ctx)
		}
		return nil, err
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return fail(fmt.Errorf("wasm: %w", err))
	}
	if _, err := rt.NewHostModuleBuilder("arc").
		NewFunctionBuilder().WithFunc(b.hostCall).Export("host").
		NewFunctionBuilder().WithFunc(b.hostRead).Export("host_read").
		Instantiate(ctx); err != nil {
		return fail(fmt.Errorf("wasm: %w", err))
	}
	compiled, err := rt.CompileModule(ctx, b.wasm)
	if err != nil {
		return fail(fmt.Errorf("wasm: the backend does not compile: %w", err))
	}
	exports := compiled.ExportedFunctions()
	for _, name := range []string{"_initialize", "arc_alloc", "arc_call"} {
		if _, ok := exports[name]; !ok {
			return fail(fmt.Errorf("wasm: the backend does not export %s — it is not built against the plugin SDK", name))
		}
	}
	return &warm{rt: rt, compiled: compiled, cache: dirCache}, nil
}

// acquire returns the compiled code, compiling it first if there is none, and
// counts the caller as inside it until release. Callers that arrive while a
// compile is running wait for that one rather than starting another.
func (b *Backend) acquire(ctx context.Context) (*warm, error) {
	b.mu.Lock()
	for {
		switch {
		case b.closed:
			b.mu.Unlock()
			return nil, errRemoved
		case b.err != nil:
			b.mu.Unlock()
			return nil, b.err
		case b.warm != nil:
			b.users++
			if b.idle != nil {
				b.idle.Stop()
				b.idle = nil
			}
			w := b.warm
			b.mu.Unlock()
			return w, nil
		case b.compiling != nil:
			done := b.compiling
			b.mu.Unlock()
			select {
			case <-done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			b.mu.Lock()
		default:
			done := make(chan struct{})
			b.compiling = done
			b.mu.Unlock()
			w, err := b.build()
			b.mu.Lock()
			b.compiling = nil
			close(done)
			switch {
			case err != nil:
				b.err = err
			case b.closed:
				closeWarm(w)
			default:
				b.warm = w
			}
		}
	}
}

// release ends one caller's stay inside the compiled code, and starts the
// clock that gives the code back if nobody comes.
func (b *Backend) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.users--
	b.armIdle()
}

// armIdle starts the countdown to eviction when nothing is using the code.
// The caller holds the lock.
func (b *Backend) armIdle() {
	after := b.engine.limits.IdleEvict
	if b.users > 0 || b.warm == nil || b.closed || after < 0 {
		return
	}
	if b.idle != nil {
		b.idle.Stop()
	}
	b.idle = time.AfterFunc(after, b.evict)
}

// evict gives the compiled code back, unless a call arrived in the meantime.
func (b *Backend) evict() {
	b.mu.Lock()
	if b.users > 0 || b.warm == nil || b.closed {
		b.mu.Unlock()
		return
	}
	w := b.warm
	b.warm, b.idle = nil, nil
	b.mu.Unlock()
	closeWarm(w)
	// The native code is unmapped by the runtime's Close; what the compile
	// left in the Go heap is the collector's to take, and the process is
	// otherwise idle, which is why it is being evicted.
	debug.FreeOSMemory()
}

func closeWarm(w *warm) {
	_ = w.rt.Close(context.Background())
	if w.cache != nil {
		_ = w.cache.Close(context.Background())
	}
}

// Ready compiles the module if that has not been done, and reports how it
// went. An installer calls it before it commits.
func (b *Backend) Ready(ctx context.Context) error {
	if _, err := b.acquire(ctx); err != nil {
		return err
	}
	b.release()
	return nil
}

// Close releases the compiled code and the bytes. Calls in flight finish or
// fail; none starts afterwards.
func (b *Backend) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	if b.idle != nil {
		b.idle.Stop()
		b.idle = nil
	}
	w := b.warm
	b.warm, b.wasm = nil, nil
	compiling := b.compiling
	b.mu.Unlock()
	if compiling != nil {
		// Whoever compiled sees the backend closed and discards what it made.
		<-compiling
	}
	if w != nil {
		closeWarm(w)
	}
}

type callKey struct{}

type envelope struct {
	V    int             `json:"v"`
	Kind string          `json:"kind"`
	Ctx  CallInfo        `json:"ctx"`
	Arg  json.RawMessage `json:"arg,omitempty"`
}

type reply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code     string         `json:"code"`
		Message  string         `json:"message"`
		Status   int            `json:"status,omitempty"`
		Details  map[string]any `json:"details,omitempty"`
		Internal bool           `json:"internal,omitempty"`
	} `json:"error,omitempty"`
}

// Invoke runs one request on a fresh instance: it marshals arg, calls the
// guest, and unmarshals what comes back into out (nil to ignore it). state is
// what host functions find in Call.State.
func (b *Backend) Invoke(ctx context.Context, info CallInfo, kind string, arg, out, state any) error {
	w, err := b.acquire(ctx)
	if err != nil {
		return err
	}
	defer b.release()
	raw, err := json.Marshal(arg)
	if err != nil {
		return fmt.Errorf("wasm: %w", err)
	}
	req, err := json.Marshal(envelope{V: 1, Kind: kind, Ctx: info, Arg: raw})
	if err != nil {
		return fmt.Errorf("wasm: %w", err)
	}
	if len(req) > b.engine.limits.MaxMessage {
		return errors.New("wasm: the request is too large")
	}

	ctx, cancel := context.WithTimeout(ctx, b.engine.limits.CallTimeout)
	defer cancel()
	call := &Call{Ctx: ctx, Info: info, State: state}
	ctx = context.WithValue(ctx, callKey{}, call)

	// The host's real clocks (read-only) and real randomness. wazero's defaults
	// are fakes for reproducible tests: a clock that starts at 2022-01-01 and
	// ticks a millisecond per reading, and a deterministic random source. With
	// them a guest's time.Now() would stamp every row and work out every expiry
	// from the wrong year, and crypto/rand would hand every call the same
	// "random" token.
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithSysWalltime().
		WithSysNanotime().
		WithRandSource(rand.Reader).
		WithStdout(io.Discard).
		WithStderr(&call.stderr)
	mod, err := w.rt.InstantiateModule(ctx, w.compiled, cfg)
	if err != nil {
		return b.trapped(ctx, call, err)
	}
	defer mod.Close(context.Background())

	ptr, err := b.alloc(ctx, mod, len(req))
	if err != nil {
		return b.trapped(ctx, call, err)
	}
	if !mod.Memory().Write(ptr, req) {
		return errors.New("wasm: the guest's memory is smaller than its allocation")
	}
	res, err := mod.ExportedFunction("arc_call").Call(ctx, uint64(ptr), uint64(len(req)))
	if err != nil {
		return b.trapped(ctx, call, err)
	}
	respPtr, respLen := uint32(res[0]>>32), uint32(res[0])
	if int64(respLen) > int64(b.engine.limits.MaxMessage) {
		return errors.New("wasm: the response is too large")
	}
	body, ok := mod.Memory().Read(respPtr, respLen)
	if !ok {
		return errors.New("wasm: the guest returned a response outside its memory")
	}
	var r reply
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("wasm: the guest's response is not JSON: %w", err)
	}
	if !r.OK {
		if r.Error == nil {
			return &GuestError{Message: "the plugin failed without saying why"}
		}
		return &GuestError{Code: r.Error.Code, Message: r.Error.Message, Status: r.Error.Status, Details: r.Error.Details, Internal: r.Error.Internal}
	}
	if out != nil && len(r.Result) > 0 {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return fmt.Errorf("wasm: the guest's result is not what %s returns: %w", kind, err)
		}
	}
	return nil
}

func (b *Backend) alloc(ctx context.Context, mod api.Module, n int) (uint32, error) {
	res, err := mod.ExportedFunction("arc_alloc").Call(ctx, uint64(n))
	if err != nil {
		return 0, err
	}
	return uint32(res[0]), nil
}

// trapped turns whatever stopped a call into the error its caller reads: the
// caller's own cancellation as itself, everything else as a crash with what
// the guest printed before it went.
func (b *Backend) trapped(ctx context.Context, call *Call, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	slog.Warn("plugin backend crashed", "plugin", b.name, "error", err, "stderr", call.stderr.String())
	msg := err.Error()
	if s := call.stderr.String(); s != "" {
		msg += ": " + s
	}
	return fmt.Errorf("%w: %s", ErrTrap, msg)
}

// hostCall is arc.host: read a request out of guest memory, answer it, and
// write the answer where the guest said there was room — or keep it for
// host_read when there was not.
func (b *Backend) hostCall(ctx context.Context, m api.Module, reqPtr, reqLen, bufPtr, bufCap uint32) int32 {
	call, _ := ctx.Value(callKey{}).(*Call)
	var response []byte
	if call == nil {
		response = failure("internal", "a host call outside any invocation")
	} else if int64(reqLen) > int64(b.engine.limits.MaxMessage) {
		response = failure("too_large", "the host call is too large")
	} else if raw, ok := m.Memory().Read(reqPtr, reqLen); !ok {
		response = failure("internal", "the host call points outside guest memory")
	} else {
		response = b.answer(call, raw)
	}
	if uint32(len(response)) > bufCap {
		if call != nil {
			call.pending = response
		}
		return -int32(len(response))
	}
	if !m.Memory().Write(bufPtr, response) {
		return 0
	}
	return int32(len(response))
}

// hostRead is arc.host_read: the response hostCall did not have room for.
func (b *Backend) hostRead(ctx context.Context, m api.Module, bufPtr, bufCap uint32) int32 {
	call, _ := ctx.Value(callKey{}).(*Call)
	if call == nil || call.pending == nil || uint32(len(call.pending)) > bufCap {
		return -1
	}
	n := len(call.pending)
	ok := m.Memory().Write(bufPtr, call.pending)
	call.pending = nil
	if !ok {
		return -1
	}
	return int32(n)
}

func (b *Backend) answer(call *Call, raw []byte) (out []byte) {
	var req struct {
		Op  string          `json:"op"`
		Arg json.RawMessage `json:"arg"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return failure("bad_request", "the host call is not JSON")
	}
	// A host function that panics is a bug in the server, and it must not
	// take the guest's call down with a stack trace nobody asked for: the
	// guest is told, and the log has the rest.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("plugin host call panicked", "plugin", b.name, "op", req.Op, "panic", r)
			out = failure("internal", "the host could not answer that")
		}
	}()
	result, err := b.host(call, req.Op, req.Arg)
	if err != nil {
		var he *HostError
		if errors.As(err, &he) {
			return failure(he.Code, he.Message)
		}
		return failure("error", err.Error())
	}
	body, err := json.Marshal(struct {
		OK     bool `json:"ok"`
		Result any  `json:"result"`
	}{true, result})
	if err != nil {
		return failure("internal", "the host's answer cannot be encoded")
	}
	return body
}

func failure(code, message string) []byte {
	body, _ := json.Marshal(struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{false, struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message}})
	return body
}

// cappedBuffer keeps the last few kilobytes a guest wrote: enough to say why
// it crashed, not enough to be a way to fill the log.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

const stderrCap = 4 << 10

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Write(p)
	if over := c.buf.Len() - stderrCap; over > 0 {
		c.buf.Next(over)
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
