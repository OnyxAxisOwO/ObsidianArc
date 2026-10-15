package idp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A holder of a copied code who guesses the verifier wrong has not used the
// code. Spending it on that guess would let anyone who saw the code lock the
// real application out of its own sign-in.
func TestAWrongVerifierDoesNotSpendTheCode(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{Name: "Desktop", Public: true})

	verifier := "a-verifier-nobody-else-has-seen-at-all"
	sum := sha256.Sum256([]byte(verifier))
	target := authorizeURL(app.ClientID, map[string]string{
		"code_challenge":        base64.RawURLEncoding.EncodeToString(sum[:]),
		"code_challenge_method": "S256",
	})
	code, _ := codeFrom(t, h.consent(t, target, true))

	guess := h.exchange(code, app.ClientID, "", url.Values{"code_verifier": {"a-guess"}})
	if guess.Code != http.StatusBadRequest {
		t.Fatalf("wrong verifier = %d %s, want it refused", guess.Code, guess.Body.String())
	}

	real := h.exchange(code, app.ClientID, "", url.Values{"code_verifier": {verifier}})
	if real.Code != http.StatusOK {
		t.Fatalf("the real application after a wrong guess = %d %s, want tokens", real.Code, real.Body.String())
	}
}

// A spent code presented with a wrong verifier is not a replay of the
// application's exchange, so it must not revoke the tokens the application
// already holds. A replay with the right verifier still does.
func TestAGuessAgainstASpentCodeRevokesNothing(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{Name: "Desktop", Public: true})

	verifier := "another-verifier-nobody-else-has-seen-here"
	sum := sha256.Sum256([]byte(verifier))
	target := authorizeURL(app.ClientID, map[string]string{
		"code_challenge":        base64.RawURLEncoding.EncodeToString(sum[:]),
		"code_challenge_method": "S256",
	})
	code, _ := codeFrom(t, h.consent(t, target, true))

	first := h.exchange(code, app.ClientID, "", url.Values{"code_verifier": {verifier}})
	if first.Code != http.StatusOK {
		t.Fatalf("first exchange = %d %s", first.Code, first.Body.String())
	}
	tokens := decodeTokens(t, first)

	guess := h.exchange(code, app.ClientID, "", url.Values{"code_verifier": {"a-guess"}})
	if guess.Code != http.StatusBadRequest {
		t.Fatalf("guess against a spent code = %d %s, want it refused", guess.Code, guess.Body.String())
	}
	if _, err := h.store.ResolveAccess(t.Context(), tokens.AccessToken); err != nil {
		t.Errorf("a guess revoked the application's own token: %v", err)
	}

	replay := h.exchange(code, app.ClientID, "", url.Values{"code_verifier": {verifier}})
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replay with the right verifier = %d %s, want it refused", replay.Code, replay.Body.String())
	}
	if _, err := h.store.ResolveAccess(t.Context(), tokens.AccessToken); err != ErrBadToken {
		t.Error("a replay with the right verifier did not revoke the tokens it bought")
	}
}

// A database that cannot be read is the server failing. Each path answers that
// as a server fault and never as the thing it is not: bad client credentials,
// an unknown application, or a stale consent request.
func TestAStorageFailureIsNotBlamedOnTheApplicationOrThePerson(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	start := h.get(authorizeURL(app.ClientID, nil), &h.account)
	location := start.Header().Get("Location")
	if !strings.HasPrefix(location, "/oauth/consent?request=") {
		t.Fatalf("authorize sent the browser to %q, want the consent screen", location)
	}
	ticket := strings.TrimPrefix(location, "/oauth/consent?request=")
	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))

	if err := h.store.db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	token := h.exchange(code, app.ClientID, secret, nil)
	if token.Code != http.StatusInternalServerError || strings.Contains(token.Body.String(), "invalid_client") {
		t.Errorf("token with the database down = %d %s, want a 500 server_error", token.Code, token.Body.String())
	}

	revoke := h.postForm("/oauth/revoke", url.Values{
		"client_id": {app.ClientID}, "client_secret": {secret}, "token": {"anything"},
	})
	if revoke.Code != http.StatusInternalServerError {
		t.Errorf("revoke with the database down = %d %s, want a 500", revoke.Code, revoke.Body.String())
	}

	shown := h.get("/oauth/authorize?"+url.Values{
		"client_id": {app.ClientID}, "redirect_uri": {"https://wiki.example.com/callback"},
		"response_type": {"code"}, "scope": {"openid"},
	}.Encode(), &h.account)
	if got := shown.Header().Get("Location"); got != "/oauth/consent?error=failed" {
		t.Errorf("authorize with the database down sent the browser to %q, want the generic failure", got)
	}

	consent := h.get("/api/oauth/consent?request="+ticket, &h.account)
	if consent.Code != http.StatusInternalServerError {
		t.Errorf("consent with the database down = %d %s, want a 500", consent.Code, consent.Body.String())
	}
}

// A consent request that no longer stands is answered with a stable code the
// screen can translate, so a Chinese reader is not shown the English sentence.
func TestAStaleConsentRequestCarriesAStableCode(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{})

	h.consent(t, authorizeURL(app.ClientID, nil), true)
	answer := h.postJSON("/api/oauth/consent",
		map[string]any{"request": "not-a-real-ticket", "approve": true}, &h.account)
	if answer.Code != http.StatusBadRequest || !strings.Contains(answer.Body.String(), "request_stale") {
		t.Errorf("stale request = %d %s, want a 400 carrying request_stale", answer.Code, answer.Body.String())
	}
}

// An application that maps groups to roles reads the groups claim and nothing
// else. A group read that fails must fail the request: a token that quietly
// omits the claim makes the account look ungrouped to that application, and
// nothing reports it. The identity endpoint is held to the same rule.
func TestAFailedGroupReadFailsTheRequestInsteadOfDroppingTheClaim(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{Scopes: []string{"openid", "profile", "email", "groups"}})
	target := authorizeURL(app.ClientID, map[string]string{"scope": "openid profile email groups"})

	// The control: with the group readable, the claim arrives.
	code, _ := codeFrom(t, h.consent(t, target, true))
	first := h.exchange(code, app.ClientID, secret, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("token with the group readable = %d %s", first.Code, first.Body.String())
	}
	tokens := decodeTokens(t, first)
	groups, _ := h.verify(t, tokens.IDToken)["groups"].([]any)
	if len(groups) != 1 || groups[0] != "Staff" {
		t.Fatalf("groups claim = %v, want the account's group", groups)
	}

	// A row the database cannot read as a group. A missing row is a normal
	// answer and is tested separately; this is a read that went wrong. The
	// created_at column is an integer, so text in it fails the scan.
	if _, err := h.db.Exec(t.Context(),
		`UPDATE user_groups SET created_at = ? WHERE id = ?`, "not-a-number", h.account.GroupID); err != nil {
		t.Fatalf("corrupt the group row: %v", err)
	}

	code, _ = codeFrom(t, h.consent(t, target, true))
	failed := h.exchange(code, app.ClientID, secret, nil)
	if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), "server_error") {
		t.Errorf("token with the group unreadable = %d %s, want a 500 server_error", failed.Code, failed.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("userinfo with the group unreadable = %d %s, want a 500", recorder.Code, recorder.Body.String())
	}
}

// A refresh whose identity token cannot be built must fail without spending the
// token it was given. Spent first, the client's retry would present a spent
// token, which reads as a replay, and the replay revokes everything the
// application holds for the account.
func TestAFailedGroupReadDuringRefreshLeavesTheTokenUnspent(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{Scopes: []string{"openid", "profile", "email", "groups"}})
	target := authorizeURL(app.ClientID, map[string]string{"scope": "openid profile email groups"})
	code, _ := codeFrom(t, h.consent(t, target, true))
	tokens := decodeTokens(t, h.exchange(code, app.ClientID, secret, nil))

	refresh := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {app.ClientID},
		"client_secret": {secret},
	}

	// The same unreadable row as the userinfo test above. The original value
	// goes back afterwards, so the retry reads the group as it was.
	var createdAt int64
	if err := h.db.QueryRow(t.Context(),
		`SELECT created_at FROM user_groups WHERE id = ?`, h.account.GroupID).Scan(&createdAt); err != nil {
		t.Fatalf("read the group row: %v", err)
	}
	if _, err := h.db.Exec(t.Context(),
		`UPDATE user_groups SET created_at = ? WHERE id = ?`, "not-a-number", h.account.GroupID); err != nil {
		t.Fatalf("corrupt the group row: %v", err)
	}
	failed := h.postForm("/oauth/token", refresh)
	if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), "server_error") {
		t.Fatalf("refresh with the group unreadable = %d %s, want a 500 server_error", failed.Code, failed.Body.String())
	}

	if _, err := h.db.Exec(t.Context(),
		`UPDATE user_groups SET created_at = ? WHERE id = ?`, createdAt, h.account.GroupID); err != nil {
		t.Fatalf("restore the group row: %v", err)
	}
	retried := h.postForm("/oauth/token", refresh)
	if retried.Code != http.StatusOK {
		t.Fatalf("retry once the group reads again = %d %s, want the same token to refresh",
			retried.Code, retried.Body.String())
	}
}

// A group deleted out from under an account leaves nothing to name, and that is
// not a failure: the token still issues, without the claim.
func TestADeletedGroupLeavesNoGroupsClaimAndNoError(t *testing.T) {
	f := newFixture(t)
	claims := Claims{}
	account := user.User{ID: f.account.ID, GroupID: "a-group-that-was-deleted"}
	if err := f.service.fill(t.Context(), &claims, account, []string{ScopeGroups}); err != nil {
		t.Fatalf("fill for a deleted group = %v, want no error", err)
	}
	if claims.Groups != nil {
		t.Errorf("groups = %v, want no claim for a group that no longer exists", claims.Groups)
	}
}

// The browser is shown the same generic failure at each step of authorize, so
// the log is the only place an outage in one step can be told from another.
func TestAnAuthorizeStorageFaultIsLoggedWithItsStep(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	t.Run("consent", func(t *testing.T) {
		logged.Reset()
		h := newHarness(t)
		app, _ := h.app(t, CreateAppInput{})
		if _, err := h.db.Exec(t.Context(), `DROP TABLE oauth_grants`); err != nil {
			t.Fatalf("drop consent table: %v", err)
		}

		shown := h.get(authorizeURL(app.ClientID, nil), &h.account)
		if got := shown.Header().Get("Location"); got != "/oauth/consent?error=failed" {
			t.Fatalf("authorize sent the browser to %q, want the generic failure", got)
		}
		if !strings.Contains(logged.String(), "idp: authorize") || !strings.Contains(logged.String(), "step=consent") {
			t.Errorf("log = %q, want the consent step recorded", logged.String())
		}
	})

	t.Run("approve", func(t *testing.T) {
		logged.Reset()
		h := newHarness(t)
		app, _ := h.app(t, CreateAppInput{})
		// The account agreed before, so the next visit goes straight to
		// approval, which is the step that needs the codes table.
		h.consent(t, authorizeURL(app.ClientID, nil), true)
		if _, err := h.db.Exec(t.Context(), `DROP TABLE oauth_codes`); err != nil {
			t.Fatalf("drop codes table: %v", err)
		}

		shown := h.get(authorizeURL(app.ClientID, nil), &h.account)
		if got := shown.Header().Get("Location"); got != "/oauth/consent?error=failed" {
			t.Fatalf("authorize sent the browser to %q, want the generic failure", got)
		}
		if !strings.Contains(logged.String(), "idp: authorize") || !strings.Contains(logged.String(), "step=approve") {
			t.Errorf("log = %q, want the approve step recorded", logged.String())
		}
	})
}
