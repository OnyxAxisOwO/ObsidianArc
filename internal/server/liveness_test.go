package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// A reader arriving while the figures are being rebuilt is answered from the
// previous snapshot. Before the cache was lock-free, that reader waited on the
// refresh, and every model listing with it, for as long as the database took.
func TestLivenessReadersAreNotQueuedBehindARefresh(t *testing.T) {
	var cache livenessCache
	ctx := context.Background()
	figures := func(context.Context) (map[string]model.Liveness, error) {
		return map[string]model.Liveness{"m": {Unstable: true}}, nil
	}
	cache.get(ctx, 1, figures)

	// A new revision makes the snapshot stale. The refresh holds until released.
	started := make(chan struct{})
	release := make(chan struct{})
	refreshed := make(chan struct{})
	go func() {
		defer close(refreshed)
		cache.get(ctx, 2, func(ctx context.Context) (map[string]model.Liveness, error) {
			close(started)
			<-release
			return figures(ctx)
		})
	}()
	<-started

	answered := make(chan map[string]model.Liveness, 1)
	go func() { answered <- cache.get(ctx, 2, figures) }()
	select {
	case got := <-answered:
		if !got["m"].Unstable {
			// Release the refresh first: Fatalf exits this goroutine, and a
			// refresh still blocked on release would outlive the test.
			close(release)
			<-refreshed
			t.Fatalf("reader got %+v, want the previous snapshot", got)
		}
	case <-time.After(5 * time.Second):
		close(release)
		<-refreshed
		t.Fatal("a reader waited behind the refresh instead of using the previous figures")
	}
	close(release)
	<-refreshed
}

// Many readers arriving on a stale revision cause one refresh, not one each,
// and a fresh snapshot is served without refreshing at all.
func TestLivenessRefreshesOncePerRevision(t *testing.T) {
	var cache livenessCache
	ctx := context.Background()
	var calls atomic.Int32
	figures := func(context.Context) (map[string]model.Liveness, error) {
		calls.Add(1)
		return map[string]model.Liveness{}, nil
	}

	done := make(chan struct{})
	for range 16 {
		go func() {
			cache.get(ctx, 7, figures)
			done <- struct{}{}
		}()
	}
	for range 16 {
		<-done
	}
	cache.get(ctx, 7, figures)
	if got := calls.Load(); got != 1 {
		t.Fatalf("refreshed %d times for one revision, want 1", got)
	}
}

// A failing database is not queried again by every listing inside the TTL,
// and the last good figures are kept meanwhile.
func TestLivenessRemembersAFailedRefresh(t *testing.T) {
	var cache livenessCache
	ctx := context.Background()
	cache.get(ctx, 1, func(context.Context) (map[string]model.Liveness, error) {
		return map[string]model.Liveness{"m": {Unstable: true}}, nil
	})

	var calls atomic.Int32
	failing := func(context.Context) (map[string]model.Liveness, error) {
		calls.Add(1)
		return nil, errors.New("database unavailable")
	}
	for range 5 {
		got := cache.get(ctx, 2, failing)
		if !got["m"].Unstable {
			t.Fatalf("a failed refresh dropped the last good figures: %+v", got)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("a failing database was queried %d times inside the TTL, want 1", got)
	}
}
