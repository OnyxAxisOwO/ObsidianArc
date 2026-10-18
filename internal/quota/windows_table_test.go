package quota

import (
	"slices"
	"testing"
)

// The window vocabulary is spelled in several tables, and a map lookup on a
// missing key is a zero, not a compile error. A kind missing from
// counterRetention would have the prune delete its current bucket; a window
// missing from summaryFloor would have the summary read only rows starting at
// now. This holds every table to the lists that drive them.
func TestEveryWindowTableCoversEveryWindow(t *testing.T) {
	for _, kind := range counterKinds {
		if counterRetention[kind] <= 0 {
			t.Errorf("counterRetention has no retention for %q", kind)
		}
	}
	for kind := range counterRetention {
		if !slices.Contains(counterKinds, kind) {
			t.Errorf("counterRetention keeps %q, which counterKinds never prunes", kind)
		}
	}
	for _, window := range AllowanceWindows {
		if summaryFloor[window] <= 0 {
			t.Errorf("summaryFloor has no floor for %q", window)
		}
		if !slices.Contains(counterKinds, window) {
			t.Errorf("allowance window %q is not a counter kind, so it is never pruned", window)
		}
		// Reserve charges every allowance window; Settle and Release walk
		// tokenWindows, so one left out of it keeps its estimate for ever.
		if !slices.Contains(tokenWindows, window) {
			t.Errorf("allowance window %q is not settled or released", window)
		}
	}
	if len(tokenWindows) == 0 || tokenWindows[0] != WindowTPM {
		t.Errorf("tokenWindows = %v, want the minute first, as the lock order requires", tokenWindows)
	}
}
