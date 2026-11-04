package mail

import (
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

// A stored secret the box cannot open, after a key rotation or damage, used
// to fail every save, including the one that types a new password. That is
// the save meant to recover from it.
func TestAnUnopenableStoredSecretDoesNotBlockASaveThatReplacesIt(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	master := []byte("test-instance-master-key")
	manager, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://example.com"}
	if err := manager.Save(ctx, cfg, "first-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	other, err := secret.New([]byte("a-different-master-key"), "obsidian-arc/smtp-password")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := other.Seal("sealed-under-another-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE mail_config SET password = ? WHERE id = 1`, sealed); err != nil {
		t.Fatalf("overwrite stored secret: %v", err)
	}

	if err := manager.Save(ctx, cfg, "", false, true); err == nil {
		t.Fatal("a save that keeps an unopenable secret succeeded")
	}
	if err := manager.Save(ctx, cfg, "replacement-password", false, true); err != nil {
		t.Fatalf("a typed replacement was refused by the secret it replaces: %v", err)
	}
	if got, set := manager.Config(); got.Password != "replacement-password" || !set {
		t.Fatalf("config after replacement = password %q, set %v", got.Password, set)
	}
}

// NewManager hands its configuration to the sender, and the sender writes the
// sender address into a header as given. Environment values never went through
// Save, so they are validated on the way in.
func TestALegacyConfigTheValidatorRefusesNeverReachesTheSender(t *testing.T) {
	sender := New(Config{})
	legacy := Config{
		Host: "smtp.example.com", Port: 587, Username: "mailer", Password: "p",
		From: "sender@example.com\r\nBcc: victim@example.com", PublicURL: "https://example.com",
	}
	if _, err := NewManager(context.Background(), mailDatabase(t), sender, legacy, []byte("test-instance-master-key")); err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if sender.Configured() {
		t.Fatal("a sender address with a line break was handed to the sender")
	}
}

// The sender refuses an address with a line break before it dials, so an
// address that somehow reaches it still cannot inject a header.
func TestSendRefusesASenderAddressWithALineBreak(t *testing.T) {
	sender := New(Config{
		Host: "127.0.0.1", Port: 2525, From: "sender@example.com\r\nBcc: victim@example.com",
	})
	err := sender.Send(context.Background(), Message{To: "reader@example.com", Subject: "s", Body: "b"})
	if err == nil || strings.Contains(err.Error(), "dial") {
		t.Fatalf("Send with a line break in the sender = %v, want a refusal before dialing", err)
	}
}

// An origin the validator refuses switches off verification links, not the
// relay or the public URL itself. The relay never reads PublicURL, so a bad
// origin in the environment used to turn every message off at boot; and plugin
// path bases resolve against the configured value, so clearing it breaks them
// even over plain HTTP.
func TestALegacyPublicURLTheValidatorRefusesKeepsMailOn(t *testing.T) {
	sender := New(Config{})
	legacy := Config{
		Host: "smtp.example.com", Port: 587, Username: "mailer", Password: "p",
		From: "sender@example.com", PublicURL: "http://example.com",
	}
	manager, err := NewManager(context.Background(), mailDatabase(t), sender, legacy, []byte("test-instance-master-key"))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if !sender.Configured() {
		t.Fatal("an insecure public URL switched the SMTP relay off")
	}
	if sender.VerificationReady() {
		t.Fatal("a refused public URL would build verification links")
	}
	if got := sender.PublicURL(); got != legacy.PublicURL {
		t.Fatalf("sender public URL = %q, want the configured value kept", got)
	}
	if got, _ := manager.Config(); got.Host != legacy.Host || got.PublicURL != legacy.PublicURL {
		t.Fatalf("manager config = host %q, public URL %q", got.Host, got.PublicURL)
	}
}

// A stored row the validator refuses after a validator change must stay on the
// admin mail page for the operator to correct, not just switch the sender off.
func TestAStoredConfigTheValidatorRefusesStaysVisibleToTheAdminForm(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	master := []byte("test-instance-master-key")
	manager, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://example.com"}
	if err := manager.Save(ctx, cfg, "stored-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	// Written past Save, the way a row saved before a validator change would be.
	if _, err := db.Exec(ctx, `UPDATE mail_config SET from_address = ? WHERE id = 1`, "sender@example.com\r\nBcc: victim@example.com"); err != nil {
		t.Fatalf("overwrite stored sender: %v", err)
	}

	sender := New(Config{})
	reloaded, err := NewManager(ctx, db, sender, Config{}, master)
	if err != nil {
		t.Fatalf("reload manager: %v", err)
	}
	if sender.Configured() {
		t.Fatal("a refused stored sender reached the sender")
	}
	got, set := reloaded.Config()
	if got.Host != cfg.Host || got.Port != cfg.Port || got.Username != cfg.Username || !set {
		t.Fatalf("admin form config = %+v, password set %v; want the stored relay kept", got, set)
	}
}

// IPv6 literals are valid relay addresses: JoinHostPort brackets them at dial
// time, and environment configuration used to reach Configured() with them
// intact. Refusing them switched mail off for any instance that set one.
func TestAnIPv6LiteralRelayIsAcceptedByTheValidator(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"2001:db8::1", "::1"} {
		if err := ValidateConfig(Config{Host: host, Port: 587, From: "sender@example.com"}); err != nil {
			t.Fatalf("ValidateConfig(%q) = %v, want an IPv6 literal accepted", host, err)
		}
	}
	for _, host := range []string{"smtp.example.com:587", "[2001:db8::1]"} {
		if err := ValidateConfig(Config{Host: host, Port: 587, From: "sender@example.com"}); err == nil {
			t.Fatalf("ValidateConfig(%q) accepted a host with a port or brackets", host)
		}
	}

	sender := New(Config{})
	legacy := Config{Host: "2001:db8::1", Port: 587, Username: "mailer", Password: "p", From: "sender@example.com"}
	manager, err := NewManager(ctx, mailDatabase(t), sender, legacy, []byte("test-instance-master-key"))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if !sender.Configured() {
		t.Fatal("an environment IPv6 relay switched mail off")
	}
	if err := manager.Save(ctx, Config{Host: "2001:db8::2", Port: 587, From: "sender@example.com", PublicURL: "https://example.com"}, "pw", false, true); err != nil {
		t.Fatalf("save with an IPv6 relay: %v", err)
	}
}
