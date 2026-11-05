package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The browser signs out on this code, so both gates must use it when a
// session ends while the tab is still open.
func TestAnAnonymousRequestIsAnsweredWithTheSessionCode(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		wrap       func(http.Handler) http.Handler
	}{
		{"user", "/api/me/things", RequireUser},
		{"admin", "/api/admin/people", RequireAdmin},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reached := false
			handler := tt.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if reached {
				t.Fatal("an anonymous request reached the handler")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %q: %v", rec.Body.String(), err)
			}
			if body.Error.Code != "session_required" {
				t.Fatalf("code = %q, want session_required", body.Error.Code)
			}
		})
	}
}
