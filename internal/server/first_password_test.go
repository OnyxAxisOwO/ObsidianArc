package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// An account opened through a provider has no password. Its first one may be
// set only from a sign-in made just now, and a refusal reaches the screen as a
// code it can translate, not as a sentence.

// withoutPassword leaves the account as a provider sign-in opens it.
func (in *instance) withoutPassword(userID string) {
	in.t.Helper()
	if _, err := in.db.Exec(context.Background(),
		`UPDATE users SET password_hash = '' WHERE id = ?`, userID); err != nil {
		in.t.Fatalf("clear the password: %v", err)
	}
}

// signedInAgo moves the account's sessions' sign-in time back.
func (in *instance) signedInAgo(userID string, by time.Duration) {
	in.t.Helper()
	if _, err := in.db.Exec(context.Background(),
		`UPDATE sessions SET created_at = ? WHERE user_id = ?`,
		time.Now().Add(-by).UnixMilli(), userID); err != nil {
		in.t.Fatalf("age the session: %v", err)
	}
}

func TestAFirstPasswordFromASignInMadeJustNowIsAccepted(t *testing.T) {
	in := newInstance(t)
	in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	in.withoutPassword(member.userID)

	set := in.do(http.MethodPost, "/api/profile/password", map[string]string{
		"new_password": "a-brand-new-password",
	}, member)
	if set.Code != http.StatusNoContent {
		t.Fatalf("first password from a sign-in made just now = %d %s", set.Code, set.Body.String())
	}
	in.login("member", "a-brand-new-password")
}

func TestAFirstPasswordFromAnOldSignInIsRefusedWithACode(t *testing.T) {
	in := newInstance(t)
	in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	in.withoutPassword(member.userID)
	in.signedInAgo(member.userID, 16*time.Minute)

	refused := in.do(http.MethodPost, "/api/profile/password", map[string]string{
		"new_password": "a-brand-new-password",
	}, member)
	if refused.Code != http.StatusForbidden || !strings.Contains(refused.Body.String(), "reauth_required") {
		t.Fatalf("first password from an old sign-in = %d %s, want 403 reauth_required",
			refused.Code, refused.Body.String())
	}

	var hash string
	if err := in.db.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = ?`, member.userID).Scan(&hash); err != nil {
		t.Fatalf("read the password: %v", err)
	}
	if hash != "" {
		t.Error("a refused first password was written anyway")
	}
}
