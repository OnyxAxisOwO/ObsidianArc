package main

import (
	"testing"
	"time"
)

// "stopped" is logged only when the background loops really returned, so
// waitForJanitor has to say which happened. A loop that never returns is the
// case the bounded wait exists for, and the case where the log must not claim
// the final writes landed.
func TestWaitForJanitorReportsWhetherTheLoopsStopped(t *testing.T) {
	previous := janitorGrace
	janitorGrace = 50 * time.Millisecond
	t.Cleanup(func() { janitorGrace = previous })

	returned := make(chan struct{})
	close(returned)
	if !waitForJanitor(func() {}, returned) {
		t.Error("loops that had returned were reported as still running")
	}

	stuck := make(chan struct{})
	if waitForJanitor(func() {}, stuck) {
		t.Error("loops that never returned were reported as stopped")
	}
}

// The database may close only after the loops have returned: their final
// request-log write begins after cancellation. The close is handed in as a
// function so that the order can be checked where it happens.
func TestStopBackgroundThenCloseClosesOnlyAfterTheLoopsReturn(t *testing.T) {
	previous := janitorGrace
	janitorGrace = 5 * time.Second
	t.Cleanup(func() { janitorGrace = previous })

	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()
	closes := 0
	closeDB := func() error {
		select {
		case <-done:
		default:
			t.Error("database closed while a background loop was still running")
		}
		closes++
		return nil
	}
	if !stopBackgroundThenClose(func() {}, done, closeDB) {
		t.Error("loops that had returned were reported as still running")
	}
	if closes != 1 {
		t.Errorf("database closed %d times, want once", closes)
	}
}

// A stuck loop must not keep the database open past the grace period: the
// process is stopping either way.
func TestStopBackgroundThenCloseStillClosesWhenALoopIsStuck(t *testing.T) {
	previous := janitorGrace
	janitorGrace = 50 * time.Millisecond
	t.Cleanup(func() { janitorGrace = previous })

	stuck := make(chan struct{})
	closed := false
	if stopBackgroundThenClose(func() {}, stuck, func() error { closed = true; return nil }) {
		t.Error("loops that never returned were reported as stopped")
	}
	if !closed {
		t.Error("database left open behind a stuck loop")
	}
}
