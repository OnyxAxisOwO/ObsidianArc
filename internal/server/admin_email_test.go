package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// An address an administrator typed is not one its owner proved.
//
// The owner's own form withdraws the confirmation and drops whatever links are
// outstanding when the address moves. This one reaches the store directly and
// used to do neither: an administrator could hand any account an address it
// had never seen, already marked confirmed, and a link issued for the address
// being left stayed open behind it.
//
// It matters wherever "confirmed, on a domain the operator allowed" is being
// read as evidence of anything — which is what the two settings are for.
func TestAnAddressAnAdministratorTypedIsNotConfirmed(t *testing.T) {
	in := newInstance(t, func(cfg *config.Config) {
		// Configured, and pointed at a port that refuses: VerificationRequired
		// asks whether mail is possible, not whether it arrives.
		cfg.Mail.Host = "127.0.0.1"
		cfg.Mail.Port = 1
		cfg.Mail.From = "arc@example.com"
		cfg.Mail.PublicURL = "https://arc.example.com"
	})

	admin := in.register("founder", "a-good-password")
	if res := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"registration.verify_email": "true"}, admin); res.Code != http.StatusOK {
		t.Fatalf("turn verification on: %d %s", res.Code, res.Body.String())
	}

	member := in.register("member", "another-password")

	// Confirmed the ordinary way first, so the withdrawal below has something
	// to withdraw.
	confirm := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"email": "member@example.com"}, admin)
	if confirm.Code != http.StatusOK {
		t.Fatalf("set the address: %d %s", confirm.Code, confirm.Body.String())
	}

	moved := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"email": "somewhere-else@example.com"}, admin)
	if moved.Code != http.StatusOK {
		t.Fatalf("move the address: %d %s", moved.Code, moved.Body.String())
	}

	var payload struct {
		User struct {
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
		} `json:"user"`
	}
	if err := json.NewDecoder(moved.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.User.Email != "somewhere-else@example.com" {
		t.Fatalf("address = %q", payload.User.Email)
	}
	if payload.User.EmailVerified {
		t.Error("an address nobody proved is marked confirmed")
	}

	// And the same for a nickname change that touches no address: it must not
	// unconfirm anybody.
	settled := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"nickname": "Renamed"}, admin)
	if settled.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", settled.Code, settled.Body.String())
	}
	var second struct {
		User struct {
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
		} `json:"user"`
	}
	if err := json.NewDecoder(settled.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if second.User.Email != "somewhere-else@example.com" {
		t.Errorf("a rename moved the address to %q", second.User.Email)
	}
	if second.User.EmailVerified != payload.User.EmailVerified {
		t.Error("a rename changed whether the address was confirmed")
	}
}
