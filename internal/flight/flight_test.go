package flight

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Callers that arrive while a call runs share it, and all of them see the one
// result.
func TestCallersArrivingTogetherShareOneCall(t *testing.T) {
	var g Group
	var runs atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})

	const callers = 10
	var wg sync.WaitGroup
	results := make([]any, callers)
	shared := make([]bool, callers)
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], _, shared[0] = g.Do("k", func() (any, error) {
			runs.Add(1)
			close(started)
			<-release
			return "answer", nil
		})
	}()
	<-started
	for i := 1; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _, shared[i] = g.Do("k", func() (any, error) {
				runs.Add(1)
				return "second", nil
			})
		}()
	}
	// Every waiter has to be counted before the call ends, or it would run
	// one of its own; there is nothing to wait on but the count itself.
	for {
		g.mu.Lock()
		dups := g.calls["k"].dups
		g.mu.Unlock()
		if dups == callers-1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	if n := runs.Load(); n != 1 {
		t.Fatalf("the function ran %d times", n)
	}
	for i := range callers {
		if results[i] != "answer" || !shared[i] {
			t.Fatalf("caller %d got %v, shared %v", i, results[i], shared[i])
		}
	}
}

// An error is shared like a value, and the key is free once the call returns.
func TestTheKeyIsFreeAgainOnceTheCallReturns(t *testing.T) {
	var g Group
	boom := errors.New("boom")
	if _, err, shared := g.Do("k", func() (any, error) { return nil, boom }); !errors.Is(err, boom) || shared {
		t.Fatalf("err = %v, shared = %v", err, shared)
	}
	v, err, _ := g.Do("k", func() (any, error) { return 2, nil })
	if err != nil || v != 2 {
		t.Fatalf("a second call reused the first: %v %v", v, err)
	}
}

// A panic in the function is raised in every caller of Do, not swallowed.
func TestAPanicReachesEveryCallerOfDo(t *testing.T) {
	var g Group
	started := make(chan struct{})
	release := make(chan struct{})
	panics := make(chan any, 2)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer func() { panics <- recover() }()
		g.Do("k", func() (any, error) {
			close(started)
			<-release
			panic("broken")
		})
	}()
	<-started
	go func() {
		defer wg.Done()
		defer func() { panics <- recover() }()
		g.Do("k", func() (any, error) { return nil, nil })
	}()
	for {
		g.mu.Lock()
		dups := g.calls["k"].dups
		g.mu.Unlock()
		if dups == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	close(panics)
	for p := range panics {
		pe, ok := p.(*panicError)
		if !ok || pe.value != "broken" {
			t.Fatalf("recovered %#v", p)
		}
	}
}

// runtime.Goexit in the function ends the caller's goroutine too, and leaves
// the key free.
func TestGoexitEndsTheCallerAndFreesTheKey(t *testing.T) {
	var g Group
	done := make(chan bool)
	go func() {
		returned := false
		defer func() { done <- returned }()
		g.Do("k", func() (any, error) {
			runtime.Goexit()
			return nil, nil
		})
		returned = true
	}()
	if <-done {
		t.Fatal("Do returned after the function called Goexit")
	}
	if v, _, _ := g.Do("k", func() (any, error) { return 1, nil }); v != 1 {
		t.Fatalf("the key stayed taken: %v", v)
	}
}

// DoChan delivers to every channel, and a caller that stops listening blocks
// nobody: each channel has room for its result.
func TestDoChanDeliversToCallersThatStoppedListening(t *testing.T) {
	var g Group
	release := make(chan struct{})
	first := g.DoChan("k", func() (any, error) {
		<-release
		return "answer", nil
	})
	second := g.DoChan("k", func() (any, error) { return "other", nil })
	close(release)

	// Nobody reads first; second still gets its answer.
	got := <-second
	if got.Val != "answer" || got.Err != nil || !got.Shared {
		t.Fatalf("second got %+v", got)
	}
	if got := <-first; got.Val != "answer" {
		t.Fatalf("first got %+v", got)
	}
}
