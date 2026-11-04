package idp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// issueSession runs the browser round trip once and returns the tokens it
// produced. The account has agreed already after the first time, so the
// consent screen is skipped from then on.
func issueSession(t *testing.T, h *harness, app App, secret string) Tokens {
	t.Helper()
	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	return decodeTokens(t, h.exchange(code, app.ClientID, secret, nil))
}

func refreshWith(h *harness, app App, secret, token string) *httptest.ResponseRecorder {
	return h.postForm("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token},
		"client_id":     {app.ClientID},
		"client_secret": {secret},
	})
}

func accessStatus(h *harness, token string) int {
	request := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	return recorder.Code
}

// A refresh token whose lifetime has ended, and which was never rotated, is
// not a copy in somebody's hands. It is only old. Presenting it used to revoke
// every token the application held for the account, including the session the
// person is using now.
func TestAnExpiredRefreshTokenDoesNotRevokeTheLiveSession(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})
	old := issueSession(t, h, app, secret)
	live := issueSession(t, h, app, secret)

	past := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := h.db.Exec(context.Background(),
		`UPDATE oauth_tokens SET refresh_expires_at = ? WHERE refresh_hash = ?`,
		past, Digest(old.RefreshToken)); err != nil {
		t.Fatalf("expire refresh token: %v", err)
	}

	if got := refreshWith(h, app, secret, old.RefreshToken); got.Code != http.StatusBadRequest {
		t.Fatalf("expired refresh token = %d %s, want it refused", got.Code, got.Body.String())
	}
	if status := accessStatus(h, live.AccessToken); status != http.StatusOK {
		t.Errorf("the live session after an expired refresh token = %d, want it untouched", status)
	}
}

// A refresh token belongs to the application it was issued to. Presented by
// another application it is refused before it is spent, so the owner's next
// refresh still works. Spending it first let anyone holding a copy make the
// owner's refresh read as a replay.
func TestARefreshTokenPresentedByAnotherApplicationIsNotSpent(t *testing.T) {
	h := newHarness(t)
	owner, ownerSecret := h.app(t, CreateAppInput{Name: "Owner"})
	other, otherSecret := h.app(t, CreateAppInput{Name: "Other"})
	tokens := issueSession(t, h, owner, ownerSecret)

	if got := refreshWith(h, other, otherSecret, tokens.RefreshToken); got.Code != http.StatusBadRequest {
		t.Fatalf("another application's refresh = %d %s, want it refused", got.Code, got.Body.String())
	}
	if got := refreshWith(h, owner, ownerSecret, tokens.RefreshToken); got.Code != http.StatusOK {
		t.Fatalf("the owner's refresh after the refusal = %d %s, want it to work", got.Code, got.Body.String())
	}
}
