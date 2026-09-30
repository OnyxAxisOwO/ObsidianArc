package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	guestOnce sync.Once
	guestWasm []byte
	guestErr  error
)

func TestMain(m *testing.M) {
	ShareCompiledCode()
	os.Exit(m.Run())
}

// guest builds testdata/guest once for the whole run. It needs the Go
// toolchain, which a machine running these tests has by definition.
func guest(t *testing.T) []byte {
	t.Helper()
	guestOnce.Do(func() {
		dir, err := os.MkdirTemp("", "arcguest")
		if err != nil {
			guestErr = err
			return
		}
		out := filepath.Join(dir, "guest.wasm")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./testdata/guest")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if b, err := cmd.CombinedOutput(); err != nil {
			guestErr = errors.New(string(b))
			return
		}
		guestWasm, guestErr = os.ReadFile(out)
	})
	if guestErr != nil {
		t.Fatalf("building the test guest: %v", guestErr)
	}
	return guestWasm
}

func load(t *testing.T, limits Limits, host HostFunc) *Backend {
	t.Helper()
	if host == nil {
		host = func(*Call, string, json.RawMessage) (any, error) { return nil, nil }
	}
	b := NewEngine(limits).Load("demo", guest(t), host)
	t.Cleanup(b.Close)
	if err := b.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestACallGoesInAndOutAndReachesTheHost(t *testing.T) {
	var seen []string
	b := load(t, Limits{}, func(c *Call, op string, arg json.RawMessage) (any, error) {
		seen = append(seen, op+" "+c.Info.Plugin)
		return map[string]string{"said": "hello"}, nil
	})
	var out struct {
		Plugin string          `json:"plugin"`
		Arg    json.RawMessage `json:"arg"`
		Host   map[string]string
	}
	err := b.Invoke(t.Context(), CallInfo{Plugin: "demo", Lang: "zh"}, "echo", map[string]int{"n": 7}, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Plugin != "demo" || string(out.Arg) != `{"n":7}` || out.Host["said"] != "hello" {
		t.Fatalf("out = %+v", out)
	}
	if len(seen) != 1 || seen[0] != "echo demo" {
		t.Fatalf("host saw %v", seen)
	}
}

func TestAHostAnswerBiggerThanTheGuestsBufferStillArrives(t *testing.T) {
	big := strings.Repeat("x", 300_000)
	b := load(t, Limits{}, func(*Call, string, json.RawMessage) (any, error) { return big, nil })
	var n int
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "big", nil, &n, nil); err != nil {
		t.Fatal(err)
	}
	if n != len(big)+2 { // the JSON string's quotes
		t.Fatalf("the guest read %d bytes of a %d-byte answer", n, len(big)+2)
	}
}

func TestPerCallStateReachesTheHostAndIsPerCall(t *testing.T) {
	b := load(t, Limits{}, func(c *Call, op string, _ json.RawMessage) (any, error) {
		counter := c.State.(*int32)
		return atomic.AddInt32(counter, 1), nil
	})
	for i := 0; i < 3; i++ {
		var counter int32
		var got []int
		if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "twice", nil, &got, &counter); err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("call %d: counted %v; the state was shared between calls", i, got)
		}
	}
}

func TestAGuestsRefusalAndAHostsRefusalAreTheirOwnErrors(t *testing.T) {
	b := load(t, Limits{}, func(*Call, string, json.RawMessage) (any, error) {
		return nil, &HostError{Code: "denied", Message: "not granted"}
	})
	err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "guesterr", nil, nil, nil)
	var guestErr *GuestError
	if !errors.As(err, &guestErr) || guestErr.Code != "nope" || guestErr.Message != "the guest says no" {
		t.Fatalf("err = %v", err)
	}
	var told string
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "hosterr", nil, &told, nil); err != nil {
		t.Fatal(err)
	}
	if told != "denied: not granted" {
		t.Fatalf("the guest was told %q", told)
	}
}

func TestAHostFunctionThatPanicsDoesNotTakeTheCallDown(t *testing.T) {
	b := load(t, Limits{}, func(*Call, string, json.RawMessage) (any, error) { panic("server bug") })
	var told string
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "hosterr", nil, &told, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(told, "internal:") {
		t.Fatalf("the guest was told %q", told)
	}
}

func TestACrashIsAnErrorWithWhatTheGuestPrinted(t *testing.T) {
	b := load(t, Limits{}, nil)
	err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "panic", nil, nil, nil)
	if !errors.Is(err, ErrTrap) || !strings.Contains(err.Error(), "boom from the guest") {
		t.Fatalf("err = %v", err)
	}
	// And the backend is still good for the next call.
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "guesterr", nil, nil, nil); err == nil || errors.Is(err, ErrTrap) {
		t.Fatalf("after a crash: %v", err)
	}
}

func TestACallThatNeverReturnsIsCutOffAndOthersCarryOn(t *testing.T) {
	b := load(t, Limits{CallTimeout: 300 * time.Millisecond}, nil)
	start := time.Now()
	err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "spin", nil, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the spin ran for %s", took)
	}
	var n int
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "env", nil, &n, nil); err != nil {
		t.Fatalf("the backend did not survive a timeout: %v", err)
	}
}

func TestTheCallersContextEndsTheCall(t *testing.T) {
	b := load(t, Limits{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)
	if err := b.Invoke(ctx, CallInfo{Plugin: "demo"}, "spin", nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestMemoryPastTheLimitIsACrashNotTheServersProblem(t *testing.T) {
	b := load(t, Limits{MemoryPages: 512}, nil) // 32 MiB; the guest asks for 300
	err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "memhog", nil, nil, nil)
	if err == nil {
		t.Fatal("a guest allocated past its limit")
	}
}

func TestTheGuestSeesNoFilesystemAndNoEnvironment(t *testing.T) {
	b := load(t, Limits{}, nil)
	var fs string
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "fs", nil, &fs, nil); err != nil {
		t.Fatal(err)
	}
	if fs != "denied" {
		t.Fatalf("the guest read the host's filesystem: %q", fs)
	}
	var env int
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "env", nil, &env, nil); err != nil {
		t.Fatal(err)
	}
	if env != 0 {
		t.Fatalf("the guest saw %d environment variables", env)
	}
}

// wazero's default clock is a fake that reads 2022-01-01 and ticks a
// millisecond a reading. A plugin that stamped a row or worked out an expiry
// with time.Now() would be wrong by years, silently, so the guest is given the
// host's clock — and its random bytes are checked to be really random while
// the two are in question.
func TestTheGuestReadsTheHostsClockAndRealRandomness(t *testing.T) {
	b := load(t, Limits{}, nil)
	before := time.Now().UnixMilli()
	var now int64
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "now", nil, &now, nil); err != nil {
		t.Fatal(err)
	}
	if after := time.Now().UnixMilli(); now < before-1 || now > after+1 {
		t.Fatalf("the guest's time.Now() = %d, outside the host's %d..%d", now, before, after)
	}
	var first, second string
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "random", nil, &first, nil); err != nil {
		t.Fatal(err)
	}
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "random", nil, &second, nil); err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 32 {
		t.Fatalf("the guest's random bytes repeat or are the wrong size: %q %q", first, second)
	}
}

func TestCallsRunInParallelOnSeparateInstances(t *testing.T) {
	b := load(t, Limits{}, func(c *Call, _ string, _ json.RawMessage) (any, error) {
		return c.Info.Plugin, nil
	})
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct{ Plugin string }
			if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "echo", i, &out, nil); err != nil || out.Plugin != "demo" {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Fatalf("%d of 24 parallel calls failed", n)
	}
}

func TestABackendThatIsNotBuiltAgainstTheSDKIsRefusedWithWhy(t *testing.T) {
	// The smallest valid module: (module), which exports nothing.
	empty := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	b := NewEngine(Limits{}).Load("bare", empty, nil)
	defer b.Close()
	err := b.Ready(t.Context())
	if err == nil || !strings.Contains(err.Error(), "arc_call") && !strings.Contains(err.Error(), "_initialize") {
		t.Fatalf("err = %v", err)
	}
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "bare"}, "echo", nil, nil, nil); err == nil {
		t.Fatal("a backend that failed to load answered a call")
	}

	garbage := NewEngine(Limits{}).Load("junk", []byte("not wasm at all"), nil)
	defer garbage.Close()
	if err := garbage.Ready(t.Context()); err == nil {
		t.Fatal("garbage compiled")
	}
}

func TestACallOnARemovedBackendFails(t *testing.T) {
	b := load(t, Limits{}, nil)
	b.Close()
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "guesterr", nil, nil, nil); err == nil {
		t.Fatal("a closed backend answered")
	}
	b.Close() // twice is fine
}

func (b *Backend) isWarm() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.warm != nil
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting until %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The compiled code is tens of megabytes a backend and what these backends serve
// is used now and then, so an idle one gives it back and the next call pays for
// compiling it again — without the caller seeing anything but the delay.
func TestAnIdleBackendGivesItsCompiledCodeBackAndTheNextCallCompilesItAgain(t *testing.T) {
	b := load(t, Limits{IdleEvict: 40 * time.Millisecond}, nil)
	waitUntil(t, "the idle backend is evicted", func() bool { return !b.isWarm() })

	var out struct{ Plugin string }
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "echo", map[string]string{"a": "b"}, &out, nil); err != nil || out.Plugin != "demo" {
		t.Fatalf("a call on an evicted backend: %+v, %v", out, err)
	}
	if !b.isWarm() {
		t.Fatal("the call did not compile it again")
	}
	waitUntil(t, "it is evicted again", func() bool { return !b.isWarm() })
}

func TestABackendThatIsNeverEvictedKeepsItsCode(t *testing.T) {
	b := load(t, Limits{IdleEvict: -1}, nil)
	time.Sleep(150 * time.Millisecond)
	if !b.isWarm() {
		t.Fatal("a backend told never to evict lost its code")
	}
}

// Taking the code out from under a call would fail a request that had done
// nothing wrong: only a backend nobody is inside is given back.
func TestACallInsideABackendKeepsItsCodeFromBeingTakenBack(t *testing.T) {
	inside, release := make(chan struct{}), make(chan struct{})
	b := load(t, Limits{IdleEvict: 20 * time.Millisecond}, func(*Call, string, json.RawMessage) (any, error) {
		close(inside)
		<-release
		return "done", nil
	})
	result := make(chan error, 1)
	go func() {
		var out map[string]any
		result <- b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "echo", nil, &out, nil)
	}()
	<-inside
	time.Sleep(200 * time.Millisecond)
	if !b.isWarm() {
		t.Fatal("the code was taken back from under a call")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("the call that was inside failed: %v", err)
	}
	waitUntil(t, "it is evicted once the call is over", func() bool { return !b.isWarm() })
}

// Calls, compiles and evictions racing each other is what a busy site with a
// short idle time would be; every call has to succeed whichever it lands in.
func TestCallsAcrossEvictionsAllSucceed(t *testing.T) {
	b := load(t, Limits{IdleEvict: 15 * time.Millisecond}, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 6; i++ {
				time.Sleep(time.Duration((g*7+i*11)%30) * time.Millisecond)
				var out struct{ Plugin string }
				if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "echo", nil, &out, nil); err != nil || out.Plugin != "demo" {
					errs <- fmt.Errorf("goroutine %d call %d: %+v, %v", g, i, out, err)
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

// A removal that lands in the middle of a compile has to leave nothing behind:
// what the compile made is thrown away, and nothing starts afterwards.
func TestClosingABackendWhileItCompilesLeavesNothingRunning(t *testing.T) {
	b := NewEngine(Limits{}).Load("demo", guest(t), func(*Call, string, json.RawMessage) (any, error) { return nil, nil })
	ready := make(chan error, 1)
	go func() { ready <- b.Ready(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	b.Close()
	<-ready
	if b.isWarm() {
		t.Fatal("a backend closed during its compile was left warm")
	}
	if err := b.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "echo", nil, nil, nil); err == nil {
		t.Fatal("a call started on a removed backend")
	}
}

func TestABackendThatDoesNotCompileSaysSoEveryTime(t *testing.T) {
	b := NewEngine(Limits{}).Load("bad", []byte("not webassembly"), func(*Call, string, json.RawMessage) (any, error) { return nil, nil })
	t.Cleanup(b.Close)
	first := b.Ready(context.Background())
	second := b.Ready(context.Background())
	if first == nil || second == nil || first.Error() != second.Error() {
		t.Fatalf("Ready = %v, then %v", first, second)
	}
}

// A compile that has to start from nothing is most of a minute on a small
// server, so what it made is kept in a directory, and a backend loaded again —
// after an eviction, a restart, an update that changed nothing — reads it. The
// directory is only ever a cache: it holds files, not the compiled code that a
// closed backend gave back.
func TestCompiledCodeIsKeptInTheCacheDirectoryAndReadBack(t *testing.T) {
	old := shared.Swap(nil)
	t.Cleanup(func() { shared.Store(old) })
	dir := t.TempDir()
	host := func(*Call, string, json.RawMessage) (any, error) { return nil, nil }

	first := NewEngine(Limits{CacheDir: dir}).Load("demo", guest(t), host)
	if err := first.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	first.Close()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("nothing was kept in the cache directory: %v %v", entries, err)
	}

	second := NewEngine(Limits{CacheDir: dir, IdleEvict: 30 * time.Millisecond}).Load("demo", guest(t), host)
	t.Cleanup(second.Close)
	var out struct{ Plugin string }
	if err := second.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "echo", nil, &out, nil); err != nil || out.Plugin != "demo" {
		t.Fatalf("a call on a backend compiled from the cache: %+v, %v", out, err)
	}
	waitUntil(t, "it is evicted", func() bool { return !second.isWarm() })
	if err := second.Invoke(t.Context(), CallInfo{Plugin: "demo"}, "echo", nil, &out, nil); err != nil {
		t.Fatalf("a call after eviction: %v", err)
	}
}
