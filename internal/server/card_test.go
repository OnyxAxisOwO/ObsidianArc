package server

import (
	"net/http"
	"testing"
	"time"
)

// What the account itself is offered, which is the only view that says
// whether a moved card is usable again.
func readerCards(t *testing.T, in *instance, as *session) []struct {
	ID        string `json:"id"`
	ExpiresAt int64  `json:"expires_at"`
} {
	t.Helper()
	return decode[struct {
		Cards []struct {
			ID        string `json:"id"`
			ExpiresAt int64  `json:"expires_at"`
		} `json:"cards"`
	}](t, in.do(http.MethodGet, "/api/usage/cards", nil, as)).Cards
}

// Withdrawing a card, and the part that is a decision rather than a
// mechanism: the account is told nothing.
//
// Cards arrive without a message, so they leave without one. An operator
// correcting a grant they typed wrong is not making an announcement, and the
// assertion is here rather than in a comment because a notice is exactly the
// sort of thing somebody adds later meaning well.
func TestWithdrawingACardIsSilent(t *testing.T) {
	in := newInstance(t)
	admin := in.register("drop-admin", "a-good-password")
	reader := in.register("drop-reader", "a-good-password")

	expires := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	if res := in.do(http.MethodPost, "/api/admin/users/"+reader.userID+"/cards",
		map[string]any{"cards": 2, "expires_at": expires}, admin); res.Code != http.StatusCreated {
		t.Fatalf("grant cards: %d %s", res.Code, res.Body.String())
	}

	mine := readerCards(t, in, reader)
	if len(mine) != 2 {
		t.Fatalf("the owner sees %d cards, want 2", len(mine))
	}
	doomed := mine[0].ID

	dropped := in.do(http.MethodDelete,
		"/api/admin/users/"+reader.userID+"/cards/"+doomed, nil, admin)
	if dropped.Code != http.StatusNoContent {
		t.Fatalf("withdraw: %d %s", dropped.Code, dropped.Body.String())
	}

	left := readerCards(t, in, reader)
	if len(left) != 1 || left[0].ID == doomed {
		t.Fatalf("the owner still sees %+v", left)
	}
	if spent := in.do(http.MethodPost, "/api/usage/cards/"+doomed+"/use",
		map[string]any{}, reader); spent.Code != http.StatusNotFound {
		t.Fatalf("a withdrawn card was still spendable: %d %s", spent.Code, spent.Body.String())
	}

	// Nothing was posted to the one channel this product has for telling an
	// account something happened.
	feed := decode[struct {
		Announcements []struct {
			ID string `json:"id"`
		} `json:"announcements"`
	}](t, in.do(http.MethodGet, "/api/announcements", nil, reader))
	if len(feed.Announcements) != 0 {
		t.Errorf("withdrawing a card announced something: %+v", feed.Announcements)
	}

	// A spent card is a record, not a permission, and stays.
	remaining := readerCards(t, in, reader)[0].ID
	if used := in.do(http.MethodPost, "/api/usage/cards/"+remaining+"/use",
		map[string]any{}, reader); used.Code != http.StatusNoContent {
		t.Fatalf("spend the other card: %d %s", used.Code, used.Body.String())
	}
	refused := in.do(http.MethodDelete,
		"/api/admin/users/"+reader.userID+"/cards/"+remaining, nil, admin)
	if refused.Code != http.StatusConflict {
		t.Fatalf("withdrawing a spent card: %d %s", refused.Code, refused.Body.String())
	}
}

func TestNamedCodeCreationAndRedemptionEndToEnd(t *testing.T) {
	in := newInstance(t)
	admin := in.register("code-admin", "a-good-password")
	reader := in.register("code-reader", "a-good-password")

	// 1. Admin creates a named code with windows 5h and 1w.
	createRes := in.do(http.MethodPost, "/api/admin/codes", map[string]any{
		"code":      "ABCD-EFGH",
		"name":      "VIP Summer Pass",
		"windows":   []string{"5h", "1w"},
		"cards":     5,
		"card_days": 10,
	}, admin)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create code: %d %s", createRes.Code, createRes.Body.String())
	}

	// 2. Admin lists codes and verifies name and windows.
	type codeItem struct {
		Code    string   `json:"code"`
		Name    string   `json:"name"`
		Windows []string `json:"windows"`
	}
	listRes := decode[struct {
		Codes []codeItem `json:"codes"`
	}](t, in.do(http.MethodGet, "/api/admin/codes", nil, admin))
	if len(listRes.Codes) != 1 {
		t.Fatalf("codes len = %d, want 1", len(listRes.Codes))
	}
	if listRes.Codes[0].Name != "VIP Summer Pass" {
		t.Errorf("code name = %q, want 'VIP Summer Pass'", listRes.Codes[0].Name)
	}
	if len(listRes.Codes[0].Windows) != 2 || listRes.Codes[0].Windows[0] != "5h" || listRes.Codes[0].Windows[1] != "1w" {
		t.Errorf("code windows = %v, want ['5h', '1w']", listRes.Codes[0].Windows)
	}

	// 3. Reader redeems the code.
	redeemRes := in.do(http.MethodPost, "/api/usage/redeem", map[string]any{
		"code": "abcd efgh",
	}, reader)
	if redeemRes.Code != http.StatusCreated {
		t.Fatalf("redeem code: %d %s", redeemRes.Code, redeemRes.Body.String())
	}

	// 4. Reader checks available cards.
	type fullCard struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Windows []string `json:"windows"`
	}
	cardsList := decode[struct {
		Cards []fullCard `json:"cards"`
	}](t, in.do(http.MethodGet, "/api/usage/cards", nil, reader)).Cards
	if len(cardsList) != 1 {
		t.Fatalf("cards = %+v, want 1", cardsList)
	}
	if cardsList[0].Name != "VIP Summer Pass" {
		t.Errorf("card name = %q, want 'VIP Summer Pass'", cardsList[0].Name)
	}
	if len(cardsList[0].Windows) != 2 || cardsList[0].Windows[0] != "5h" || cardsList[0].Windows[1] != "1w" {
		t.Errorf("card windows = %v, want ['5h', '1w']", cardsList[0].Windows)
	}

	// 5. Populate counters in 5h, 1w, and 1m.
	scopeKey := "u:" + reader.userID
	now := time.Now().UnixMilli()
	if _, err := in.db.Exec(t.Context(),
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, '5h', ?, 1, 10, 0.1), (?, '1w', ?, 1, 10, 0.1), (?, '1m', ?, 1, 10, 0.1)`,
		scopeKey, now, scopeKey, now, scopeKey, now); err != nil {
		t.Fatalf("insert counters: %v", err)
	}

	// 6. Reader spends the card.
	spendRes := in.do(http.MethodPost, "/api/usage/cards/"+cardsList[0].ID+"/use", map[string]any{}, reader)
	if spendRes.Code != http.StatusNoContent {
		t.Fatalf("spend card: %d %s", spendRes.Code, spendRes.Body.String())
	}

	// 7. Verify 5h and 1w counters were cleared, but 1m is untouched.
	var count5H, count1W, count1M int
	if err := in.db.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM usage_counters WHERE scope_key = ? AND window_kind = '5h'`,
		scopeKey).Scan(&count5H); err != nil {
		t.Fatal(err)
	}
	if err := in.db.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM usage_counters WHERE scope_key = ? AND window_kind = '1w'`,
		scopeKey).Scan(&count1W); err != nil {
		t.Fatal(err)
	}
	if err := in.db.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM usage_counters WHERE scope_key = ? AND window_kind = '1m'`,
		scopeKey).Scan(&count1M); err != nil {
		t.Fatal(err)
	}
	if count5H != 0 {
		t.Errorf("5h count = %d, want 0", count5H)
	}
	if count1W != 0 {
		t.Errorf("1w count = %d, want 0", count1W)
	}
	if count1M != 1 {
		t.Errorf("1m count = %d, want 1", count1M)
	}
}
