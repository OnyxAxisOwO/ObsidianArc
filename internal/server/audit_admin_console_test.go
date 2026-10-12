package server

import (
	"net/http"
	"testing"
)

// A negative day count would be stored as an expiry of zero, which the bonus
// store reads as a credit that never expires.
func TestABonusGrantWithNegativeDaysIsRefused(t *testing.T) {
	in := newInstance(t)
	admin := in.register("neg-days-admin", "a-good-password")
	reader := in.register("neg-days-reader", "a-good-password")

	created := in.do(http.MethodPost, "/api/admin/bonus/bars", map[string]any{
		"name": "礼包", "toggle_mode": "user", "show_total": true,
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	bar := decode[struct {
		Bar struct{ ID string } `json:"bar"`
	}](t, created).Bar

	res := in.do(http.MethodPost, "/api/admin/bonus/bars/"+bar.ID+"/grants", map[string]any{
		"user_ids": []string{reader.userID}, "amount": 5, "days": -3,
	}, admin)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("a grant with negative days: %d %s, want 400", res.Code, res.Body.String())
	}
}
