package server

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// The figures readers see on every model listing. A listing is asked for on
// every page load, and the figures move on a ten-minute sweep, so they are
// cached briefly; the cache is what keeps the listing off the database.
const (
	livenessTTL     = 30 * time.Second
	livenessTimeout = 10 * time.Second
)

type livenessSnapshot struct {
	at       time.Time
	revision uint64
	seen     map[string]model.Liveness
}

// livenessCache holds the last snapshot in an atomic pointer rather than
// behind a mutex. A mutex held across the refresh would make every listing
// that arrived meanwhile queue behind the database, including the ones that
// only needed the figure already in hand.
type livenessCache struct {
	snapshot   atomic.Pointer[livenessSnapshot]
	refreshing atomic.Bool
}

// get returns the figures for a revision of the health data, refreshing them
// when the snapshot is stale. Only one caller refreshes at a time; the others
// are answered from the previous snapshot without waiting.
func (c *livenessCache) get(
	ctx context.Context,
	revision uint64,
	refresh func(context.Context) (map[string]model.Liveness, error),
) map[string]model.Liveness {
	prev := c.snapshot.Load()
	if prev != nil && prev.revision == revision && time.Since(prev.at) < livenessTTL {
		return prev.seen
	}
	if !c.refreshing.CompareAndSwap(false, true) {
		// Before the first snapshot there is nothing to show, so a listing
		// that arrives during the first load goes without figures rather than
		// waiting for them.
		if prev != nil {
			return prev.seen
		}
		return nil
	}
	defer c.refreshing.Store(false)

	// The flag is released only after the previous refresh has stored its
	// snapshot, so a reader that loaded nil before that store can win the flag
	// afterwards. Looking again here answers it from that snapshot instead of
	// rebuilding a figure that is already fresh.
	if cur := c.snapshot.Load(); cur != nil && cur.revision == revision && time.Since(cur.at) < livenessTTL {
		return cur.seen
	}

	// Detached from the request. The figures are shared by every reader, so
	// one client going away must not leave the next listing to rebuild them,
	// and the timeout keeps a slow database from holding the refresh forever.
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), livenessTimeout)
	defer cancel()
	seen, err := refresh(refreshCtx)
	if err != nil {
		slog.ErrorContext(ctx, "liveness for readers", "error", err)
		// The last good figures stay in place, and the failure is remembered
		// for the same TTL, so an outage is not re-queried by every listing.
		var kept map[string]model.Liveness
		if prev != nil {
			kept = prev.seen
		}
		c.snapshot.Store(&livenessSnapshot{at: time.Now(), revision: revision, seen: kept})
		return kept
	}
	c.snapshot.Store(&livenessSnapshot{at: time.Now(), revision: revision, seen: seen})
	return seen
}
