package card

import (
	"slices"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
)

// A card names windows by the strings quota stores, but this package keeps its
// own list so it need not import quota. A window renamed on one side would
// leave cards that validate and then reset nothing, so the names are held equal
// here, where a test may import what the package may not.
func TestCardWindowsAreQuotaWindows(t *testing.T) {
	want := make([]string, 0, len(quota.AllowanceWindows))
	for _, window := range quota.AllowanceWindows {
		want = append(want, string(window))
	}
	if !slices.Equal(ValidWindows, want) {
		t.Fatalf("card.ValidWindows = %v, quota.AllowanceWindows = %v", ValidWindows, want)
	}
}
