package reqlog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// fillAggregationSlots takes every slot the process-wide aggregation semaphore
// has, without assuming how many that is, and releases them when the test ends.
func fillAggregationSlots(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	for {
		release, err := httpx.AcquireAggregationSlot(ctx)
		if err != nil {
			return
		}
		t.Cleanup(release)
	}
}

func isBusyAggregation(err error) bool {
	var apiErr *httpx.Error
	return errors.As(err, &apiErr) && apiErr.Code == "too_many_concurrent_aggregations"
}

// Facets and List read the whole table when no time bound is given, so they
// take an aggregation slot like the dashboard's queries. When every slot is
// busy they answer with the same busy error rather than stacking scans on the
// database pool, and they run again once a slot is free.
func TestFacetsAndListWaitForAnAggregationSlot(t *testing.T) {
	store := newStore(t)

	fillAggregationSlots(t)
	if _, err := store.Facets(t.Context(), 0); !isBusyAggregation(err) {
		t.Fatalf("Facets with every slot busy returned %v, want the busy error", err)
	}
	if _, _, err := store.List(t.Context(), Filter{}); !isBusyAggregation(err) {
		t.Fatalf("List with every slot busy returned %v, want the busy error", err)
	}
}

func TestFacetsAndListRunOnceASlotIsFree(t *testing.T) {
	store := newStore(t)
	if _, err := store.Facets(t.Context(), 0); err != nil {
		t.Fatalf("Facets with a free slot: %v", err)
	}
	if _, _, err := store.List(t.Context(), Filter{}); err != nil {
		t.Fatalf("List with a free slot: %v", err)
	}
}
