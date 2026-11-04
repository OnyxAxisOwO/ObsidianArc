package server

import (
	"net/http"
	"testing"
	"time"
)

// A card restores the allowance and never the per-minute counters. Those are
// what protect the upstream provider from a burst, and the account that has
// just hit the burst limit must not be let straight back in by spending a card.
func TestASpentCardRestoresTheAllowanceButNotTheMinuteCounters(t *testing.T) {
	in := newInstance(t)
	admin := in.register("card-admin", "a-good-password")
	reader := in.register("card-reader", "a-good-password")
	expires := time.Now().Add(48 * time.Hour).Truncate(time.Second).UnixMilli()

	grant := in.do(http.MethodPost, "/api/admin/users/"+reader.userID+"/cards", map[string]any{
		"name":       "Full",
		"windows":    []string{"full"},
		"cards":      1,
		"expires_at": expires,
	}, admin)
	if grant.Code != http.StatusCreated {
		t.Fatalf("grant full card: %d %s", grant.Code, grant.Body.String())
	}

	cards := decode[struct {
		Cards []struct {
			ID string `json:"id"`
		} `json:"cards"`
	}](t, in.do(http.MethodGet, "/api/usage/cards", nil, reader)).Cards
	if len(cards) != 1 {
		t.Fatalf("cards = %+v, want 1", cards)
	}

	scope := "u:" + reader.userID
	now := time.Now().UnixMilli()
	if _, err := in.db.Exec(t.Context(),
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, '5h', ?, 10, 500, 0.5), (?, 'rpm', ?, 60, 0, 0), (?, 'tpm', ?, 0, 90000, 0)`,
		scope, now, scope, now, scope, now); err != nil {
		t.Fatalf("insert counters: %v", err)
	}

	if res := in.do(http.MethodPost, "/api/usage/cards/"+cards[0].ID+"/use", map[string]any{}, reader); res.Code != http.StatusNoContent {
		t.Fatalf("spend full card: %d %s", res.Code, res.Body.String())
	}

	counts := map[string]int{}
	for _, kind := range []string{"5h", "rpm", "tpm"} {
		var n int
		if err := in.db.QueryRow(t.Context(),
			`SELECT COUNT(*) FROM usage_counters WHERE scope_key = ? AND window_kind = ?`,
			scope, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[kind] = n
	}
	if counts["5h"] != 0 {
		t.Errorf("the allowance window was not restored by a full card (%d rows left)", counts["5h"])
	}
	if counts["rpm"] != 1 || counts["tpm"] != 1 {
		t.Errorf("a card cleared the per-minute counters: rpm=%d tpm=%d, want 1 each", counts["rpm"], counts["tpm"])
	}
}
