package leaderboard

import (
	"strconv"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
)

// A stored size that does not parse falls back to fallbackSize, and that is
// meant to be the board an untouched instance draws. The two are written in
// different packages, so they are held together here.
func TestFallbackSizeIsTheRegisteredDefault(t *testing.T) {
	want, err := strconv.Atoi(settings.Defaults[settings.LeaderboardSize])
	if err != nil {
		t.Fatalf("default leaderboard size %q: %v", settings.Defaults[settings.LeaderboardSize], err)
	}
	if fallbackSize != want {
		t.Fatalf("fallbackSize = %d, registered default = %d", fallbackSize, want)
	}
}

// Ties on requests break on tokens and then on the key, so the profiles the
// cached board collects and the board drawn from it rank the same accounts.
func TestSortByRequestsBreaksTiesOnTokensThenKey(t *testing.T) {
	rows := []usage.Breakdown{
		{Key: "c", Requests: 5, TotalTokens: 10},
		{Key: "b", Requests: 5, TotalTokens: 10},
		{Key: "a", Requests: 5, TotalTokens: 20},
		{Key: "d", Requests: 9, TotalTokens: 1},
	}
	sortByRequests(rows)
	got := ""
	for _, row := range rows {
		got += row.Key
	}
	if got != "dabc" {
		t.Fatalf("order = %q, want %q", got, "dabc")
	}
}
