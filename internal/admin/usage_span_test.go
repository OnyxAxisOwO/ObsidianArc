package admin

import (
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
)

func TestTheSummaryDrawsShortWindowsHourlyAndLongOnesDaily(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name   string
		filter usage.Filter
		span   time.Duration
		bucket time.Duration
	}{
		{"all time", usage.Filter{}, 4 * day, day},
		{"a closed day", usage.Filter{Since: now.Add(-day).UnixMilli(), Until: now.UnixMilli()}, day, time.Hour},
		{"a closed week", usage.Filter{Since: now.Add(-7 * day).UnixMilli(), Until: now.UnixMilli()}, 7 * day, day},
		{"open since three days", usage.Filter{Since: now.Add(-3 * day).UnixMilli()}, 3 * day, time.Hour},
		{"open since a month", usage.Filter{Since: now.Add(-30 * day).UnixMilli()}, 30 * day, day},
	}
	for _, c := range cases {
		span, bucket := summarySpan(c.filter, now)
		if span != c.span || bucket != c.bucket {
			t.Errorf("%s: span %v bucket %v, want %v and %v", c.name, span, bucket, c.span, c.bucket)
		}
	}
}
