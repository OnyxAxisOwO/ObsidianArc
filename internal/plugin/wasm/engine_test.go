package wasm

import (
	"context"
	"encoding/json"
	"errors"
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
