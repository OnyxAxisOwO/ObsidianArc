package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// With Secure cookies a sign-in state is named with the __Host- prefix, so a
// copy planted under the plain name by a neighbouring hostname is not read.
func TestASignInStateSetByAnotherHostnameIsNotReadWhenSecure(t *testing.T) {
	f := newFixture(t)
	f.configure(t, "github")
	stub(t, "github", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"a-token"}`))
	}, Identity{Subject: "4218", Login: "octocat"})
	h, mux := handlers(t, f)
	h.SecureCookie = true

	start := get(mux, "/api/auth/oauth/start/github", nil, nil)
	target, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	nonce := target.Query().Get("state")

	var state *http.Cookie
	for _, cookie := range start.Result().Cookies() {
		if cookie.Name == "__Host-"+stateCookie {
			state = cookie
		}
	}
	if state == nil {
		t.Fatalf("secure state cookie not named with the __Host- prefix: %+v", start.Result().Cookies())
	}
	if state.Path != "/" || !state.Secure || state.Domain != "" {
		t.Fatalf("state cookie = path %q secure %v domain %q, want path / secure with no domain",
			state.Path, state.Secure, state.Domain)
	}

	planted := &http.Cookie{Name: stateCookie, Value: state.Value}
	refused := get(mux, "/api/auth/oauth/callback/github?code=c&state="+nonce, []*http.Cookie{planted}, nil)
	if location := refused.Header().Get("Location"); !strings.HasPrefix(location, "/login?oauth_error=") {
		t.Fatalf("a state under the plain name was accepted: %d %q", refused.Code, location)
	}

	back := get(mux, "/api/auth/oauth/callback/github?code=c&state="+nonce, []*http.Cookie{state}, nil)
	if back.Code != http.StatusFound || back.Header().Get("Location") != "/" {
		t.Fatalf("the __Host- state was refused: %d %q", back.Code, back.Header().Get("Location"))
	}
}

// The sign-up ticket follows the same rule: a plain-named copy is not held.
func TestASignUpTicketSetByAnotherHostnameIsNotHeldWhenSecure(t *testing.T) {
	f := newFixture(t)
	h, _ := handlers(t, f)
	h.SecureCookie = true

	ticket, err := h.pending.issuePending(pending{
		Provider: "github",
		Subject:  "4218",
		Expiry:   time.Now().Add(time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}

	planted := httptest.NewRequest(http.MethodGet, "/", nil)
	planted.AddCookie(&http.Cookie{Name: pendingCookie, Value: ticket})
	if _, err := h.heldSignup(planted); err == nil {
		t.Fatal("a sign-up ticket under the plain name was held")
	}

	recorder := httptest.NewRecorder()
	h.setPending(recorder, ticket)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-"+pendingCookie ||
		cookies[0].Path != "/" || !cookies[0].Secure || cookies[0].Domain != "" {
		t.Fatalf("ticket cookie = %+v, want the __Host- name at path / with no domain", cookies)
	}

	held := httptest.NewRequest(http.MethodGet, "/", nil)
	held.AddCookie(&http.Cookie{Name: cookies[0].Name, Value: cookies[0].Value})
	if _, err := h.heldSignup(held); err != nil {
		t.Fatalf("the __Host- ticket was not held: %v", err)
	}
}
