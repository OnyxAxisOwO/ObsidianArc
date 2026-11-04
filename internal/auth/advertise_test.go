package auth

import (
	"sync"
	"testing"
)

// Advertise must never replace a block a plugin registered. The window it used
// to leave open was between its check and its write, so this runs the two
// against each other under -race for many rounds. The interleaving is narrow
// and cannot be forced from outside, so a pass is not proof on its own; the
// assertion is the one that a regression would break.
func TestAdvertiseNeverReplacesARegisteredBlock(t *testing.T) {
	h := &Handlers{}
	const name = "plugin"
	block := func(bool) map[string]any { return map[string]any{"on": true} }

	for round := 0; round < 2000; round++ {
		h.Unextend(name)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			h.Extend(name, block)
		}()
		go func() {
			defer wg.Done()
			<-start
			h.Advertise(name)
		}()
		close(start)
		wg.Wait()

		h.extMu.RLock()
		fn, ok := h.extensions[name]
		h.extMu.RUnlock()
		if !ok || fn == nil {
			t.Fatalf("round %d: the registered block was replaced (present=%v, nil=%v)", round, ok, fn == nil)
		}
	}
}
