package leaderboard

import (
	"fmt"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// tokenRows is a ledger with one account per value, in the order given.
func tokenRows(tokens ...int64) []usage.Breakdown {
	rows := make([]usage.Breakdown, len(tokens))
	for i, n := range tokens {
		rows[i] = usage.Breakdown{Key: fmt.Sprintf("acct-%d", i), TotalTokens: n, Requests: 1}
	}
	return rows
}

// The gap to the place above is the one number a reader gets that comes from
// an account the board does not list. Measured against an account past the
// last published place, it reveals that account's total, so it is only given
// when the place above is itself on the board.
func TestStandingGapNeverMeasuresAnUnlistedAccount(t *testing.T) {
	board := &cachedBoard{userRows: tokenRows(500, 400, 300, 200, 100)}
	h := &Handlers{}

	_, standing := h.projectAccounts(board, "acct-4", usage.MetricTokens, 2, settings.LeaderboardNickname)
	if standing.Rank != 5 || standing.Gap != 0 {
		t.Errorf("standing = %+v, want rank 5 with no gap: the place above is not on a board of two", standing)
	}

	_, standing = h.projectAccounts(board, "acct-2", usage.MetricTokens, 2, settings.LeaderboardNickname)
	if standing.Gap != 100 {
		t.Errorf("standing = %+v, want a gap of 100 to the listed place above", standing)
	}

	_, standing = h.projectAccounts(board, "acct-1", usage.MetricTokens, 3, settings.LeaderboardNickname)
	if standing.Gap != 100 {
		t.Errorf("standing = %+v, want a gap of 100 to the listed place above", standing)
	}
}

// Nicknames are optional, and an account that never set one used to be named
// by its login. Half of a login is a credential, so another reader sees a
// stranger's login only in handle mode, where the operator asked for it.
func TestNicknameModeNeverNamesAStrangerByLogin(t *testing.T) {
	board := &cachedBoard{
		userRows: tokenRows(300, 200),
		userInfos: map[string]user.User{
			"acct-0": {ID: "acct-0", Username: "stranger-login"},
			"acct-1": {ID: "acct-1", Username: "reader-login"},
		},
	}
	h := &Handlers{}

	entries, _ := h.projectAccounts(board, "acct-1", usage.MetricTokens, 20, settings.LeaderboardNickname)
	for _, e := range entries {
		if e.Self {
			if e.Name != "reader-login" {
				t.Errorf("the reader's own row = %+v, want the reader named", e)
			}
			continue
		}
		if e.Name != "" || e.Handle != "" {
			t.Errorf("a stranger with no nickname is named %q (handle %q), want no name", e.Name, e.Handle)
		}
	}

	entries, _ = h.projectAccounts(board, "acct-1", usage.MetricTokens, 20, settings.LeaderboardHandle)
	if entries[0].Handle != "stranger-login" {
		t.Errorf("handle mode row = %+v, want the login shown as the handle", entries[0])
	}
}
