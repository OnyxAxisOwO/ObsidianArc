package httpx

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestAggregationSemaphoreCapacityAndTimeout(t *testing.T) {
	sem := NewAggregationSemaphore(2, 50*time.Millisecond)
	ctx := context.Background()

	// Acquire slot 1
	rel1, err := sem.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire slot 1: %v", err)
	}

	// Acquire slot 2
	rel2, err := sem.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire slot 2: %v", err)
	}

	// Slot 3 should time out after 50ms and return 503
	start := time.Now()
	_, err = sem.Acquire(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected slot 3 to time out, but got nil")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("expected to wait at least 40ms, waited %v", elapsed)
	}

	var httpxErr *Error
	if !errors.As(err, &httpxErr) {
		t.Fatalf("expected httpx.Error, got %T: %v", err, err)
	}
	if httpxErr.Status != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", httpxErr.Status)
	}
	if httpxErr.Code != "too_many_concurrent_aggregations" {
		t.Errorf("expected code too_many_concurrent_aggregations, got %s", httpxErr.Code)
	}

	// Release slot 1, slot 3 should now succeed
	rel1()
	rel3, err := sem.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire slot 3 after releasing slot 1: %v", err)
	}
	rel2()
	rel3()
}

func TestAggregationSemaphoreContextCancel(t *testing.T) {
	sem := NewAggregationSemaphore(1, 2*time.Second)
	rel, err := sem.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire slot: %v", err)
	}
	defer rel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = sem.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAggregationSemaphorePreCancelledContext(t *testing.T) {
	sem := NewAggregationSemaphore(5, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sem.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled for pre-cancelled context, got %v", err)
	}
	// Verify no slot was leaked
	if len(sem.slots) != 0 {
		t.Fatalf("expected 0 slots taken, got %d", len(sem.slots))
	}
}
