package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
)

// connectAs asks for a connection the way the settings screen does: the account's
// session cookie on the request and its proof in the body. The request goes
// through the same middleware a browser's does, so the session the proof is judged
// by is the one the browser holds.
func connectAs(t *testing.T, f *fixture, mux *http.ServeMux, provider, token string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/connections/"+provider, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.AddCookie(&http.Cookie{Name: f.auth.CookieName(), Value: token})
	}
	recorder := httptest.NewRecorder()
	f.auth.Attach()(mux).ServeHTTP(recorder, request)
	return recorder
}

// redirectState is the state the address in a connect answer carries. The
// callback has to be sent back with it, and with the state cookie the answer set.
func redirectState(t *testing.T, connect *httptest.ResponseRecorder) string {
	t.Helper()
	var answer struct {
		Redirect string `json:"redirect"`
	}
	if err := json.Unmarshal(connect.Body.Bytes(), &answer); err != nil {
		t.Fatalf("connect answer %q: %v", connect.Body.String(), err)
	}
	target, err := url.Parse(answer.Redirect)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return target.Query().Get("state")
}

// A connection is a new way into an account, so the account's own proof is asked
// for before anything is started: its password where it has one. A missing or
// wrong password starts nothing, and the answer says which it was.
func TestConnectNeedsTheAccountsPassword(t *testing.T) {
	f := newFixture(t)
	f.configure(t, "github")
	_, mux := handlers(t, f)
	_, token, err := f.auth.Register(context.Background(), auth.RegisterInput{
		Username: "founder", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	for _, c := range []struct {
		name string
		body map[string]string
		want string
	}{
		{"without the password", map[string]string{}, "password_required"},
		{"with a wrong password", map[string]string{"password": "not-it-at-all"}, "current_password_wrong"},
	} {
		t.Run(c.name, func(t *testing.T) {
			response := connectAs(t, f, mux, "github", token, c.body)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), c.want) {
				t.Fatalf("connect = %d %s, want 400 %s", response.Code, response.Body.String(), c.want)
			}
			if cookieNamed(response, stateCookie) != nil {
				t.Error("a refused connection still set a state cookie")
			}
		})
	}

	right := connectAs(t, f, mux, "github", token, map[string]string{"password": "a-good-password"})
	if right.Code != http.StatusOK || cookieNamed(right, stateCookie) == nil || redirectState(t, right) == "" {
		t.Fatalf("connect with the password = %d %s", right.Code, right.Body.String())
	}

	anonymous := connectAs(t, f, mux, "github", "", map[string]string{"password": "a-good-password"})
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("connect with no session = %d, want 401", anonymous.Code)
	}
}

// An account opened through a provider has no password to prove, so its proof is a
// sign-in made just now: the rule that lets such an account set its first password.
// A session that has been open for a while proves nothing, and neither does an
// account on a request with no session at all.
func TestAProviderOnlyAccountConnectsOnlyAfterASignInMadeJustNow(t *testing.T) {
	f := newFixture(t)
	f.configure(t, "github")
	_, mux := handlers(t, f)
	ctx := context.Background()
	populate(t, f)

	account, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), cleared, "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("open the provider-only account: %v", err)
	}
	if hash, _ := f.users.PasswordHash(ctx, nil, account.ID); hash != "" {
		t.Fatal("the account has a password; this test is about one that has none")
	}

	token, session, err := f.auth.Sessions().Create(ctx, account.ID, time.Hour, "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("open a session: %v", err)
	}
	if fresh := connectAs(t, f, mux, "github", token, map[string]string{}); fresh.Code != http.StatusOK {
		t.Fatalf("a sign-in made just now = %d %s, want the connection started", fresh.Code, fresh.Body.String())
	}

	if _, err := f.db.Exec(ctx, `UPDATE sessions SET created_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), session.ID); err != nil {
		t.Fatalf("age the session: %v", err)
	}
	stale := connectAs(t, f, mux, "github", token, map[string]string{})
	if stale.Code != http.StatusForbidden || !strings.Contains(stale.Body.String(), "reauth_required") ||
		cookieNamed(stale, stateCookie) != nil {
		t.Fatalf("a sign-in an hour old = %d %s, want reauth_required and no state", stale.Code, stale.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/connections/github", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithUser(request.Context(), account))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "reauth_required") {
		t.Errorf("an account with no session on the request = %d %s, want reauth_required", recorder.Code, recorder.Body.String())
	}
}

// The navigation that used to start a link is refused, and starts nothing: no
// provider round trip, no state, and no connection. A browser still running the
// old bundle that asks for one gets its settings page back with a failure in
// the address, not a sign-in that would switch it to another account.
func TestAGetStartNeverLinksAnAccount(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	_, mux := handlers(t, f)
	ctx := context.Background()
	asker, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "member", Password: "another-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	begun := get(mux, "/api/auth/oauth/start/github?link=1&next="+url.QueryEscape("/admin/providers?panel=settings#models"), nil, &asker)
	if want, location := "/admin/providers?panel=settings&oauth_error=failed#models", begun.Header().Get("Location"); location != want {
		t.Fatalf("start with link=1 = %q, want it refused back to %q", location, want)
	}
	if cookieNamed(begun, stateCookie) != nil {
		t.Error("a refused start still set a state cookie")
	}
	if connections, _, _ := f.service.Connections(ctx, asker.ID); len(connections) != 0 {
		t.Fatalf("a start with link=1 connected %+v to the account that asked", connections)
	}
}

// A provider this server has switched off cannot be connected, and the answer says
// so before anything is asked of the provider.
func TestConnectRefusesAProviderThatIsOff(t *testing.T) {
	f := newFixture(t)
	_, mux := handlers(t, f)
	_, token, err := f.auth.Register(context.Background(), auth.RegisterInput{
		Username: "founder", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	response := connectAs(t, f, mux, "github", token, map[string]string{"password": "a-good-password"})
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "unavailable") ||
		cookieNamed(response, stateCookie) != nil {
		t.Fatalf("connect with the provider off = %d %s", response.Code, response.Body.String())
	}
}
