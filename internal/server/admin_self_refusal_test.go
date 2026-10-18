package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A refusal to act on one's own account carries a code, so the screen words it
// in the reader's language. The sentence alone is English, and a Chinese screen
// must not show it.
func TestSelfRefusalsCarryACode(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	second := in.register("second-admin", "another-password")
	if res := in.do(http.MethodPatch, "/api/admin/users/"+second.userID,
		map[string]any{"role": "super_admin"}, founder); res.Code != http.StatusOK {
		t.Fatalf("promote second administrator: %d %s", res.Code, res.Body.String())
	}

	// With a second super administrator the last-administrator rule does not
	// apply, so each request reaches the refusal it is named for.
	cases := []struct {
		name   string
		method string
		body   any
		code   string
	}{
		{"ban", http.MethodPatch, map[string]any{"status": "disabled", "ban_reason": "myself"}, "cannot_ban_self"},
		{"delete", http.MethodDelete, nil, "cannot_delete_self"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := in.do(c.method, "/api/admin/users/"+founder.userID, c.body, founder)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("%s oneself: %d %s, want 400", c.name, res.Code, res.Body.String())
			}
			var payload struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error.Code != c.code {
				t.Errorf("code = %q, want %q", payload.Error.Code, c.code)
			}
		})
	}
}
