package chat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Both the decoding bound (one place) and the /v1 body bound (four) are this
// one map now, so it is held here to the figure it is given, across keys,
// with cancelled waiters in the mix, and to leaving nothing behind.
func TestKeyedPlacesHoldEachKeyToItsFigure(t *testing.T) {
	var places KeyedPlaces
	const per = 2

	var wg sync.WaitGroup
	inside := map[string]*atomic.Int32{"a": {}, "b": {}}
	var worst atomic.Int32
	for i := range 40 {
		key := "a"
		if i%2 == 1 {
			key = "b"
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			if i%5 == 0 {
				// Some give up while they wait, which is the path that has to
				// leave the refcount right without ever holding a place.
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Millisecond)
				defer cancel()
			}
			release, err := places.Acquire(ctx, key, per)
			if err != nil {
				return
			}
			defer release()
			now := inside[key].Add(1)
			for {
				seen := worst.Load()
				if now <= seen || worst.CompareAndSwap(seen, now) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			inside[key].Add(-1)
			release()
		}()
	}
	wg.Wait()

	if got := worst.Load(); got > per {
		t.Errorf("%d requests held one key's places at once, want at most %d", got, per)
	}
	if got := places.Tracked(); got != 0 {
		t.Errorf("%d keys still tracked after every request ended", got)
	}
}
