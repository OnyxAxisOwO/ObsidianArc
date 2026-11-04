package server

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
)

// The state is what a reader sees as a warning, so each branch is pinned at
// its edge. The warn_below of 0 cases are the ones that used to degrade any
// model under 95 percent, against the setting's own "0 never warns".
func TestUptimeStateOf(t *testing.T) {
	cases := []struct {
		name      string
		rate      health.Rate
		warnBelow int
		want      string
	}{
		{"no answers in the hour is an outage", health.Rate{OK: 0, Total: 4}, 90, "down"},
		{"the healthy share is up", health.Rate{OK: 99, Total: 100}, 99, "up"},
		{"below warn_below is degraded", health.Rate{OK: 85, Total: 100}, 90, "degraded"},
		{"exactly at warn_below is up", health.Rate{OK: 90, Total: 100}, 90, "up"},
		{"a share under the healthy line is degraded when warned about", health.Rate{OK: 98, Total: 100}, 99, "degraded"},
		{"zero never warns, far below the healthy line", health.Rate{OK: 50, Total: 100}, 0, "up"},
		{"zero never warns, just below the healthy line", health.Rate{OK: 985, Total: 1000}, 0, "up"},
		{"a negative warn_below never warns either", health.Rate{OK: 10, Total: 100}, -1, "up"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uptimeStateOf(c.rate, c.warnBelow); got != c.want {
				t.Errorf("uptimeStateOf(%+v, %d) = %q, want %q", c.rate, c.warnBelow, got, c.want)
			}
		})
	}
}
