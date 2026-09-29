package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A cookie that resolves to no session is cleared by the lookup — which is
// how to tell whether a request paid for one. The bundle's files are the same
// for everybody and must not; an API request must.
func TestStaticAssetsSkipTheSessionLookup(t *testing.T) {
	in := newInstance(t)
	stale := &http.Cookie{Name: "obsidian_session", Value: "no-such-session-token"}

	cleared := func(path string) bool {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(stale)
		recorder := httptest.NewRecorder()
		in.handler.ServeHTTP(recorder, request)
		for _, cookie := range recorder.Result().Cookies() {
			if cookie.Name == "obsidian_session" && cookie.MaxAge < 0 {
				return true
			}
		}
		return false
	}

	for _, path := range []string{"/assets/index-anything.js", "/favicon.ico", "/robots.txt"} {
		if cleared(path) {
			t.Errorf("GET %s looked the session up", path)
		}
	}
	for _, path := range []string{"/api/auth/me", "/", "/settings"} {
		if !cleared(path) {
			t.Errorf("GET %s did not look the session up", path)
		}
	}

	// Still behind the security headers: skipping the lookup is not skipping
	// the policy.
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/index-anything.js", nil))
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Error("a static asset was served without the content security policy")
	}
}
