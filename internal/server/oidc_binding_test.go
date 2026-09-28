package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The OIDC binding policy through the assembled server: the wiring is where
// the gate, the account payload's own field and the connect flow's callback
// meet, and none of the three is visible from inside internal/oauth alone.

// stubOIDCProvider answers the token exchange with one fixed subject.
// Discovery is not under test here — the settings point straight at it, the
// same shortcut internal/oauth's own package tests take.
func stubOIDCProvider(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a-token","token_type":"Bearer"}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"55512345","preferred_username":"qq_55512345"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// doWithExtraCookies is in.do with cookies beyond the session — the OIDC
// connect flow's own state cookie rides beside it on the callback request.
func (in *instance) doWithExtraCookies(method, path string, as *session, extra ...*http.Cookie) *httptest.ResponseRecorder {
	in.t.Helper()
	request := httptest.NewRequest(method, path, nil)
	if as != nil {
		request.AddCookie(as.cookie)
	}
	for _, cookie := range extra {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

func TestOIDCBindingPolicyThroughTheWiring(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")

	stub := stubOIDCProvider(t)

	configure := map[string]string{
		"oauth.oidc_enabled":       "true",
		"oauth.oidc_client_id":     "a-client-id",
		"oauth.oidc_client_secret": "a-client-secret",
		"oauth.oidc_auth_url":      stub.URL + "/authorize",
		"oauth.oidc_token_url":     stub.URL + "/token",
		"oauth.oidc_userinfo_url":  stub.URL + "/userinfo",
	}
	if response := in.do(http.MethodPut, "/api/admin/settings", configure, founder); response.Code != http.StatusOK {
		t.Fatalf("configure oidc: %d %s", response.Code, response.Body.String())
	}

	// Nobody is held until the policy itself is switched on.
	if code := in.do(http.MethodGet, "/api/conversations", nil, member).Code; code != http.StatusOK {
		t.Fatalf("held before the policy was ever turned on: %d", code)
	}

	policy := map[string]string{"oauth.oidc_require_for_all": "true"}
	if response := in.do(http.MethodPut, "/api/admin/settings", policy, founder); response.Code != http.StatusOK {
		t.Fatalf("turn on the policy: %d %s", response.Code, response.Body.String())
	}

	// Every account that predates the switch, including the one that just
	// turned it on, is held now.
	held := in.do(http.MethodGet, "/api/conversations", nil, founder)
	if held.Code != http.StatusForbidden || !strings.Contains(held.Body.String(), "oidc_binding_required") {
		t.Fatalf("an unconnected account under the policy: %d %s", held.Code, held.Body.String())
	}
	if !strings.Contains(in.do(http.MethodGet, "/api/auth/me", nil, founder).Body.String(), `"oidc_binding_required":true`) {
		t.Fatal("the account payload does not say it must bind")
	}

	// A handful of endpoints stay reachable regardless, so the browser can
	// draw the binding screen and start the connect flow.
	if code := in.do(http.MethodGet, "/api/site", nil, founder).Code; code != http.StatusOK {
		t.Fatalf("the site endpoint is held: %d", code)
	}
	if code := in.do(http.MethodGet, "/api/auth/oauth/connections", nil, founder).Code; code != http.StatusOK {
		t.Fatalf("the connections endpoint is held: %d", code)
	}

	// A GitHub connection would not satisfy this policy.
	if code := in.do(http.MethodGet, "/api/auth/oauth/start/github?link=1", nil, founder).Code; code != http.StatusForbidden {
		t.Fatalf("a non-OIDC connect route reached past the gate: %d", code)
	}

	// The OIDC connect flow itself is always reachable, and completing it
	// lifts the gate.
	start := in.do(http.MethodGet, "/api/auth/oauth/start/oidc?link=1", nil, founder)
	if start.Code != http.StatusFound {
		t.Fatalf("start oidc link: %d %s", start.Code, start.Body.String())
	}
	cookies := start.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("no state cookie from start: %+v", cookies)
	}
	stateCookie := cookies[0]
	target, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorise url: %v", err)
	}
	nonce := target.Query().Get("state")

	callbackPath := "/api/auth/oauth/callback/oidc?code=c&state=" + nonce
	callback := in.doWithExtraCookies(http.MethodGet, callbackPath, founder, stateCookie)
	if callback.Code != http.StatusFound || !strings.HasPrefix(callback.Header().Get("Location"), "/settings?oauth=connected") {
		t.Fatalf("finish the link: %d %s", callback.Code, callback.Header().Get("Location"))
	}

	if code := in.do(http.MethodGet, "/api/conversations", nil, founder).Code; code != http.StatusOK {
		t.Fatalf("still held after connecting: %d", code)
	}
	if strings.Contains(in.do(http.MethodGet, "/api/auth/me", nil, founder).Body.String(), `"oidc_binding_required":true`) {
		t.Fatal("the account payload still says it must bind after connecting")
	}

	// A second account, never having connected, is still held.
	if code := in.do(http.MethodGet, "/api/conversations", nil, member).Code; code != http.StatusForbidden {
		t.Fatalf("a different unconnected account is not held: %d", code)
	}
}

// The next= parameter the binding gate itself sends the browser to /bind-oidc
// with rides through the connect flow and back, so completing it lands where
// the gate wanted rather than at the settings screen.
func TestOIDCBindingConnectHonoursNext(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	stub := stubOIDCProvider(t)
	configure := map[string]string{
		"oauth.oidc_enabled":         "true",
		"oauth.oidc_client_id":       "a-client-id",
		"oauth.oidc_client_secret":   "a-client-secret",
		"oauth.oidc_auth_url":        stub.URL + "/authorize",
		"oauth.oidc_token_url":       stub.URL + "/token",
		"oauth.oidc_userinfo_url":    stub.URL + "/userinfo",
		"oauth.oidc_require_for_all": "true",
	}
	if response := in.do(http.MethodPut, "/api/admin/settings", configure, founder); response.Code != http.StatusOK {
		t.Fatalf("configure: %d %s", response.Code, response.Body.String())
	}

	start := in.do(http.MethodGet, "/api/auth/oauth/start/oidc?link=1&next="+url.QueryEscape("/bind-oidc?next=%2Fsettings"), nil, founder)
	if start.Code != http.StatusFound {
		t.Fatalf("start: %d %s", start.Code, start.Body.String())
	}
	stateCookie := start.Result().Cookies()[0]
	target, _ := url.Parse(start.Header().Get("Location"))
	nonce := target.Query().Get("state")

	callback := in.doWithExtraCookies(http.MethodGet, "/api/auth/oauth/callback/oidc?code=c&state="+nonce, founder, stateCookie)
	location := callback.Header().Get("Location")
	if callback.Code != http.StatusFound || !strings.HasPrefix(location, "/bind-oidc?") || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("finish the link with next: %d %s", callback.Code, location)
	}
}
