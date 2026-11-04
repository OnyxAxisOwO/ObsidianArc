package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// A link a member asked for on their own form belongs to the address it was
// sent to. An administrator moving the address settles the move through the same
// auth method the member's form uses, so the link has to go here as well.
func TestAnAdministratorsAddressChangeRevokesTheMembersOutstandingLink(t *testing.T) {
	in := newInstance(t, func(cfg *config.Config) {
		// Configured, and pointed at a port that refuses: VerificationRequired
		// asks whether mail is possible, not whether it arrives.
		cfg.Mail.Host = "127.0.0.1"
		cfg.Mail.Port = 1
		cfg.Mail.From = "arc@example.com"
		cfg.Mail.PublicURL = "https://arc.example.com"
	})
	admin := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	if res := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"registration.verify_email": "true"}, admin); res.Code != http.StatusOK {
		t.Fatalf("turn verification on: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"email": "member@example.com"}, admin); res.Code != http.StatusOK {
		t.Fatalf("set the address: %d %s", res.Code, res.Body.String())
	}

	now := time.Now().UnixMilli()
	if _, err := in.db.Exec(t.Context(),
		`INSERT INTO email_verifications (id, user_id, email, expires_at, created_at, code_digest, code_expires_at, code_attempts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		"outstanding-link", member.userID, "member@example.com",
		now+int64(time.Hour/time.Millisecond), now, "outstanding-digest", now+int64(10*time.Minute/time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	if res := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"email": "somewhere-else@example.com"}, admin); res.Code != http.StatusOK {
		t.Fatalf("move the address: %d %s", res.Code, res.Body.String())
	}

	var outstanding int
	if err := in.db.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM email_verifications WHERE user_id = ?`, member.userID).Scan(&outstanding); err != nil {
		t.Fatal(err)
	}
	if outstanding != 0 {
		t.Errorf("%d link(s) for the old address survived the administrator's change", outstanding)
	}
}
