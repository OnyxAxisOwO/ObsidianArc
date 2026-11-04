package idp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The discovery document names this instance's own address, which comes from
// the Host header when no public URL is configured. A shared cache that does
// not key on Host would hand one host's endpoints to whoever asks under
// another, for five minutes at a time.
func TestTheDiscoveryDocumentIsCachedPerHost(t *testing.T) {
	h := newHarness(t)
	response := h.get("/.well-known/openid-configuration", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("discovery = %d", response.Code)
	}
	if vary := strings.Join(response.Header().Values("Vary"), ","); !strings.Contains(vary, "Host") {
		t.Errorf("Vary = %q, want Host named so a cache keys on it", vary)
	}
}

// The identity endpoint returns the account's email, name and groups. It is
// outside the /api and /v1 prefixes the security headers cover, so it has to
// say no-store itself.
func TestTheIdentityEndpointIsNeverStored(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})
	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	tokens := decodeTokens(t, h.exchange(code, app.ClientID, secret, nil))

	request := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("userinfo = %d %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on account details", got)
	}
}
