package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
)

// A compile that panics fails every caller waiting on it, at once. Before, the
// handoff was never closed: the caller behind the first one waited until its own
// deadline, and the backend could never compile again.
func TestACompilePanicFailsEveryCallerWaitingOnItAtOnce(t *testing.T) {
	compileHook = func() {
		time.Sleep(20 * time.Millisecond)
		panic("the compiler gave up")
	}
	t.Cleanup(func() { compileHook = nil })
	b := NewEngine(Limits{}).Load("demo", guest(t), func(*Call, string, json.RawMessage) (any, error) { return nil, nil })
	t.Cleanup(b.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					results <- fmt.Errorf("panicked: %v", r)
				}
			}()
			results <- b.Ready(ctx)
		}()
	}
	for range 2 {
		if err := <-results; err == nil {
			t.Fatal("a caller waiting on a failed compile was told it succeeded")
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("callers waited %v for a compile that had already failed", elapsed)
	}
}

// A panic that unwinds out of build after the runtime exists must still close
// it: nothing holds a runtime that was never returned, and Close cannot reach
// one. The panic's text stays out of the error as well, since the install dialog
// shows that error.
func TestACompilePanicAfterTheRuntimeIsMadeClosesIt(t *testing.T) {
	const panicText = "the runtime was made and then the compile gave up"
	var made wazero.Runtime
	runtimeHook = func(rt wazero.Runtime) {
		made = rt
		panic(panicText)
	}
	t.Cleanup(func() { runtimeHook = nil })
	module := guest(t)
	b := NewEngine(Limits{}).Load("demo", module, nil)
	t.Cleanup(b.Close)

	w, err := b.buildRecovering(module)
	if err == nil || w != nil {
		t.Fatalf("a compile that panicked returned %v, %v", w, err)
	}
	if strings.Contains(err.Error(), panicText) {
		t.Fatalf("the panic's text reached the error: %v", err)
	}
	if _, err := made.CompileModule(t.Context(), module); err == nil {
		t.Fatal("the runtime a panicking compile made was left open")
	}
}
