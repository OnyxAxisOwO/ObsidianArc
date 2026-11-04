package server

import (
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// registerWithEmail is register for a server that asks for an address: an
// account made with verification on starts unconfirmed, which is the state
// the gate is about.
func registerWithEmail(t *testing.T, in *instance, username, password string) *session {
	t.Helper()
	response := in.do(http.MethodPost, "/api/auth/register", map[string]string{
		"username": username, "password": password, "email": username + "@example.com",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", username, response.Code, response.Body.String())
	}
	reply := decode[struct {
		User struct {
			ID            string `json:"id"`
			EmailVerified bool   `json:"email_verified"`
		} `json:"user"`
	}](t, response)
	if reply.User.EmailVerified {
		t.Fatalf("%s was confirmed at registration", username)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "obsidian_session" && cookie.Value != "" {
			return &session{cookie: cookie, userID: reply.User.ID, password: password}
		}
	}
	t.Fatalf("register %s returned no session cookie", username)
	return nil
}

// An unconfirmed address cannot send a message, upload a picture or check in,
// because each of those spends something the operator pays for. Importing a
// file writes a conversation for every thread in it, so it is held to the same
// gate. The console's import command reaches the same handler, so one test
// covers both paths.
func TestAnUnverifiedAccountCannotImportAFile(t *testing.T) {
	in := newInstance(t, func(cfg *config.Config) {
		// Configured, so VerificationRequired is true; see admin_email_test.go.
		cfg.Mail.Host = "127.0.0.1"
		cfg.Mail.Port = 1
		cfg.Mail.From = "arc@example.com"
		cfg.Mail.PublicURL = "https://arc.example.com"
	})
	admin := in.register("founder", "a-good-password")
	// An account registered while verification is off is confirmed already, so
	// the unconfirmed one is registered after the setting is turned on.
	confirmed := in.register("confirmed", "another-password")

	if res := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"registration.verify_email": "true"}, admin); res.Code != http.StatusOK {
		t.Fatalf("turn verification on: %d %s", res.Code, res.Body.String())
	}
	unconfirmed := registerWithEmail(t, in, "unconfirmed", "yet-another-password")

	document := map[string]any{
		"obsidian_arc_export": 1,
		"conversations": []map[string]any{{
			"title":    "Imported",
			"messages": []map[string]any{{"role": "user", "content": "hello"}},
		}},
	}

	// The confirmed account's import is accepted. That shows the document is
	// valid, so the refusal below is the gate and nothing else.
	if res := in.do(http.MethodPost, "/api/account/import", document, confirmed); res.Code != http.StatusOK {
		t.Fatalf("confirmed account's import: %d %s", res.Code, res.Body.String())
	}

	res := in.do(http.MethodPost, "/api/account/import", document, unconfirmed)
	if res.Code != http.StatusForbidden {
		t.Fatalf("unconfirmed import: %d %s, want 403", res.Code, res.Body.String())
	}
	reply := decode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, res)
	if reply.Error.Code != "email_unverified" {
		t.Fatalf("error code %q, want email_unverified", reply.Error.Code)
	}
}
