package server

import (
	"net/http"
	"testing"
)

// The browser draws the sign-up challenge from turnstile_on_signup, and the server
// checks it only when the operator's switch or a saved site key says so. On a
// default instance the stored mode is Turnstile, but the switch is off and no key
// is saved, so nothing is checked. Advertising the widget then hands the visitor a
// challenge with nothing to draw, so the advertisement follows the check.
func TestTheSignUpChallengeIsAdvertisedOnlyWhenItIsChecked(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	advertised := func() bool {
		t.Helper()
		response := in.do(http.MethodGet, "/api/site", nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET /api/site: %d %s", response.Code, response.Body.String())
		}
		payload := decode[map[string]any](t, response)
		on, ok := payload["turnstile_on_signup"].(bool)
		if !ok {
			t.Fatal("GET /api/site has no turnstile_on_signup")
		}
		return on
	}
	if advertised() {
		t.Fatal("a default instance advertises a sign-up challenge that the server does not check")
	}

	response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"turnstile.on_signup": "true",
	}, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT /api/admin/settings: %d %s", response.Code, response.Body.String())
	}
	if !advertised() {
		t.Fatal("the switch is on and the server checks the challenge, but it is not advertised")
	}
}
