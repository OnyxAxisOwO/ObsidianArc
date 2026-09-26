package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestAdminMailSettingsEnableVerificationWithoutRestart(t *testing.T) {
	in := newInstance(t)
	administrator := in.register("founder", "a-good-password")

	saved := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "127.0.0.1", "port": 1, "from": "arc@example.com",
		"public_url": "https://arc.example.com", "password": "mail-secret",
	}, administrator)
	if saved.Code != http.StatusOK {
		t.Fatalf("save mail: %d %s", saved.Code, saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), "mail-secret") {
		t.Fatal("mail password was returned by the save endpoint")
	}
	shown := in.do(http.MethodGet, "/api/admin/mail", nil, administrator)
	if shown.Code != http.StatusOK {
		t.Fatalf("read mail: %d %s", shown.Code, shown.Body.String())
	}
	if strings.Contains(shown.Body.String(), "mail-secret") {
		t.Fatal("mail password was returned by the read endpoint")
	}
	configuration := decode[struct {
		PasswordSet bool `json:"password_set"`
	}](t, shown)
	if !configuration.PasswordSet {
		t.Fatal("saved password was not reported as configured")
	}

	// The same public address builds verification links and names this
	// instance to OAuth/OpenID clients. A stale boot-time URL splits them.
	discovery := in.do(http.MethodGet, "/.well-known/openid-configuration", nil, nil)
	if discovery.Code != http.StatusOK {
		t.Fatalf("discovery: %d %s", discovery.Code, discovery.Body.String())
	}
	issuer := decode[struct {
		Issuer string `json:"issuer"`
	}](t, discovery)
	if issuer.Issuer != "https://arc.example.com" {
		t.Fatalf("issuer = %q, want saved public URL", issuer.Issuer)
	}

	policy := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"registration.verify_email": "true"}, administrator)
	if policy.Code != http.StatusOK {
		t.Fatalf("enable verification: %d %s", policy.Code, policy.Body.String())
	}

	// A verification rule that still lets somebody register without any
	// mailbox is a bypass, even if the admin omitted require_email.
	noEmail := in.do(http.MethodPost, "/api/auth/register", map[string]string{
		"username": "without-email", "password": "another-password",
	}, nil)
	if noEmail.Code != http.StatusBadRequest {
		t.Fatalf("signup without email: %d %s, want 400", noEmail.Code, noEmail.Body.String())
	}

	registered := in.do(http.MethodPost, "/api/auth/register", map[string]string{
		"username": "member", "password": "another-password", "email": "member@example.com",
	}, nil)
	if registered.Code != http.StatusCreated {
		t.Fatalf("signup: %d %s", registered.Code, registered.Body.String())
	}
	account := decode[struct {
		User struct {
			EmailVerified bool `json:"email_verified"`
		} `json:"user"`
	}](t, registered)
	if account.User.EmailVerified {
		t.Fatal("new account was verified without opening the email")
	}
}
