package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every administrative endpoint sits behind RequireAdmin, so an open admin tab
// whose session ended is answered here. The page keys its sign-out on the code.
func TestAnAnonymousAdministrativeRequestIsAnsweredWithTheSessionCode(t *testing.T) {
	reached := false
	handler := RequireAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/people", nil))

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
}

// A request with no session is answered with the code the browser signs out on.
// The generic "unauthorized" would leave an open tab on its signed-in screen.
func TestAnAnonymousRequestIsAnsweredWithTheSessionCode(t *testing.T) {
	reached := false
	handler := RequireUser(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/me/things", nil))

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
}
