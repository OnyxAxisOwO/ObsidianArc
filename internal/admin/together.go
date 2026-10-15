package admin

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// readsAtOnce is how many of one screen's reads run side by side. The
// dashboard and the usage summary each ask a dozen independent questions of
// the ledger, and asked one after another they add up to seconds. A few at a
// time rather than all at once, because each holds a database connection
// while it runs: SQLite's pool is four by default, and a chat turn waiting
// behind a chart is worse than a chart that takes a little longer.
const readsAtOnce = 3

// together runs independent reads concurrently and returns the first error
// once every one of them has finished — so none is still writing into a
// result the caller has already moved on from.
func together(reads ...func() error) error {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	slots := make(chan struct{}, readsAtOnce)
	for _, read := range reads {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			// The handler's recovery runs on the serving goroutine only, so a
			// panic in a read would end the process rather than this request.
			// Registered last, it runs first: the error is recorded before the
			// slot is released and the wait group is done.
			defer func() {
				if p := recover(); p != nil {
					slog.Error("admin: a read panicked", "panic", p, "stack", string(debug.Stack()))
					mu.Lock()
					if first == nil {
						first = fmt.Errorf("admin: read panicked: %v", p)
					}
					mu.Unlock()
				}
			}()
			if err := read(); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return first
}
