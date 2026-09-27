package httpx

import (
	"context"
	"sync"
	"time"
)

// AggregationSemaphore caps concurrent heavy database aggregations to protect the
// database connection pool from being starved by simultaneous analytics queries.
type AggregationSemaphore struct {
	slots       chan struct{}
	waitTimeout time.Duration
}

// NewAggregationSemaphore creates a semaphore with the specified slot capacity and wait timeout.
func NewAggregationSemaphore(capacity int, waitTimeout time.Duration) *AggregationSemaphore {
	return &AggregationSemaphore{
		slots:       make(chan struct{}, capacity),
		waitTimeout: waitTimeout,
	}
}

// GlobalAggregationSemaphore is shared by leaderboard and usage analytics queries across the process.
var GlobalAggregationSemaphore = NewAggregationSemaphore(3, 2*time.Second)

// AcquireAggregationSlot waits up to 2 seconds to acquire a concurrency slot in the global
// aggregation semaphore. If slots remain busy past 2 seconds, it yields HTTP 503 with
// the machine-readable code "too_many_concurrent_aggregations".
func AcquireAggregationSlot(ctx context.Context) (func(), error) {
	return GlobalAggregationSemaphore.Acquire(ctx)
}

func (s *AggregationSemaphore) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	timer := time.NewTimer(s.waitTimeout)
	defer timer.Stop()

	select {
	case s.slots <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-s.slots
			})
		}, nil
	case <-timer.C:
		return nil, UnavailableCode("too_many_concurrent_aggregations", "The server is currently busy processing heavy queries. Please retry in a few moments.")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
