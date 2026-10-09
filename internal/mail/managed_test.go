package mail

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func mailDatabase(t *testing.T) *database.DB {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "mail.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestManagedPasswordIsSealedAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	master := []byte("test-instance-master-key")
	legacy := Config{
		Host: "legacy.example.com", Port: 587, Username: "legacy@example.com",
		Password: "legacy-password", PublicURL: "https://legacy.example.com",
	}
	sender := New(legacy)
	manager, err := NewManager(ctx, db, sender, legacy, master)
	if err != nil {
		t.Fatalf("load absent override: %v", err)
	}
	if got, set := manager.Config(); got.Password != legacy.Password || !set {
		t.Fatalf("initial config = %+v, password_set %v; want legacy password", got, set)
	}

	cfg := Config{
		Host: "smtp.example.net", Port: 465, Username: "mailer@example.net",
		From: "notifications@example.net", ImplicitTLS: true,
		PublicURL: "https://app.example.net",
	}
	const password = "secret-smtp-password-7319"
	if err := manager.Save(ctx, cfg, password, false, true); err != nil {
		t.Fatalf("save database override: %v", err)
	}
	var sealed []byte
	if err := db.QueryRow(ctx, `SELECT password FROM mail_config WHERE id = 1`).Scan(&sealed); err != nil {
		t.Fatalf("read sealed password: %v", err)
	}
	if bytes.Contains(sealed, []byte(password)) {
		t.Fatal("the database contains the SMTP password in plaintext")
	}
	box, err := secret.New(master, "obsidian-arc/smtp-password")
	if err != nil {
		t.Fatalf("create matching box: %v", err)
	}
	opened, err := box.Open(sealed)
	if err != nil || opened != password {
		t.Fatalf("open stored password = %q, %v; want original password", opened, err)
	}

	// The process can restart with changed environment values; the saved row
	// remains authoritative until an operator changes it.
	secondSender := New(Config{})
	second, err := NewManager(ctx, db, secondSender, Config{Host: "ignored.example.com", Port: 587}, master)
	if err != nil {
		t.Fatalf("reload database override: %v", err)
	}
	loaded, set := second.Config()
	if loaded.Host != cfg.Host || loaded.Password != password || !set {
		t.Fatalf("reloaded config = %+v, password_set %v", loaded, set)
	}
	if secondSender.PublicURL() != cfg.PublicURL || !secondSender.VerificationReady() {
		t.Fatalf("sender did not start with the loaded database config: %+v", secondSender.config())
	}
}

func TestEmptyPasswordKeepsSecretUntilExplicitlyCleared(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	manager, err := NewManager(ctx, db, New(Config{}), Config{}, []byte("test-instance-master-key"))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://example.com"}
	if err := manager.Save(ctx, cfg, "first-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if err := manager.Save(ctx, cfg, "", false, true); err != nil {
		t.Fatalf("save without replacement password: %v", err)
	}
	if got, set := manager.Config(); got.Password != "first-password" || !set {
		t.Fatalf("empty password replaced the stored secret: got %q, set %v", got.Password, set)
	}
	if err := manager.Save(ctx, cfg, "ignored-password", true, true); err != nil {
		t.Fatalf("clear password: %v", err)
	}
	if got, set := manager.Config(); got.Password != "" || set {
		t.Fatalf("clear_password left a secret: got %q, set %v", got.Password, set)
	}
}

// The password is posted to whichever server the host names. Keeping it
// across a change of server let whoever could edit the form collect it with
// a server of their own, so a new server, port or account needs it typed
// again; anything else keeps it.
func TestANewServerNeedsThePasswordAgain(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	manager, err := NewManager(ctx, db, New(Config{}), Config{}, []byte("test-instance-master-key"))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, Username: "mailer", From: "sender@example.com", PublicURL: "https://example.com"}
	if err := manager.Save(ctx, cfg, "the-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	for label, edit := range map[string]func(*Config){
		"host":     func(c *Config) { c.Host = "collector.example.net" },
		"port":     func(c *Config) { c.Port = 2525 },
		"username": func(c *Config) { c.Username = "someone-else" },
	} {
		moved := cfg
		edit(&moved)
		if err := manager.Save(ctx, moved, "", false, true); !errors.Is(err, ErrPasswordNeededForMove) {
			t.Errorf("%s: save without the password = %v, want ErrPasswordNeededForMove", label, err)
		}
	}
	if got, _ := manager.Config(); got.Host != "smtp.example.com" || got.Password != "the-password" {
		t.Errorf("a refused save changed the configuration: %+v", got)
	}

	moved := cfg
	moved.Host = "smtp2.example.com"
	if err := manager.Save(ctx, moved, "the-password", false, true); err != nil {
		t.Errorf("a move with the password typed again: %v", err)
	}
}

func TestStaleManagerPreservesLatestPasswordAcrossSaves(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	master := []byte("test-instance-master-key")
	firstSender, staleSender := New(Config{}), New(Config{})
	first, err := NewManager(ctx, db, firstSender, Config{}, master)
	if err != nil {
		t.Fatalf("new first manager: %v", err)
	}
	stale, err := NewManager(ctx, db, staleSender, Config{}, master)
	if err != nil {
		t.Fatalf("new stale manager: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://example.com"}
	if err := first.Save(ctx, cfg, "initial-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if err := first.Save(ctx, cfg, "rotated-password", false, true); err != nil {
		t.Fatalf("rotate password: %v", err)
	}

	// This manager has never seen the rotation. Its unrelated save must use
	// the row's current secret, and must not restore a stale value after clear.
	cfg.From = "edited@example.com"
	if err := stale.Save(ctx, cfg, "", false, true); err != nil {
		t.Fatalf("stale manager save: %v", err)
	}
	if got, set := stale.Config(); got.Password != "rotated-password" || !set || got.From != cfg.From {
		t.Fatalf("stale manager config = %+v, password_set %v", got, set)
	}

	if err := first.Save(ctx, cfg, "", true, true); err != nil {
		t.Fatalf("clear password: %v", err)
	}
	if err := stale.Save(ctx, cfg, "", false, true); err != nil {
		t.Fatalf("save after clear: %v", err)
	}
	if got, set := stale.Config(); got.Password != "" || set {
		t.Fatalf("stale manager restored a cleared password: %+v, set %v", got, set)
	}
}

func TestConcurrentManagerSavesKeepRotatedPassword(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	master := []byte("test-instance-master-key")
	first, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new first manager: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://example.com"}
	if err := first.Save(ctx, cfg, "initial-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	stale, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new second manager: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- first.Save(ctx, cfg, "rotated-password", false, true)
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- stale.Save(ctx, cfg, "", false, true)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save: %v", err)
		}
	}

	reloaded, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("reload after concurrent saves: %v", err)
	}
	if got, set := reloaded.Config(); got.Password != "rotated-password" || !set {
		t.Fatalf("concurrent saves left config %+v, password_set %v", got, set)
	}
}

func TestSenderUsesHotConfigurationUpdatesAcrossConcurrentReads(t *testing.T) {
	old := Config{Host: "smtp-old.example.com", Port: 587, From: "old@example.com", PublicURL: "https://old.example.com"}
	newConfig := Config{Host: "smtp-new.example.com", Port: 465, From: "new@example.com", ImplicitTLS: true, PublicURL: "https://new.example.com"}
	sender := New(old)
	if sender.PublicURL() != old.PublicURL {
		t.Fatalf("initial URL = %q", sender.PublicURL())
	}

	const readers = 12
	start := make(chan struct{})
	done := make(chan struct{}, readers)
	for i := 0; i < readers; i++ {
		go func() {
			<-start
			for j := 0; j < 500; j++ {
				url := sender.PublicURL()
				if url != old.PublicURL && url != newConfig.PublicURL {
					t.Errorf("read a torn URL %q", url)
					break
				}
				_ = sender.Configured()
				_ = sender.VerificationReady()
			}
			done <- struct{}{}
		}()
	}
	close(start)
	for i := 0; i < 500; i++ {
		if i%2 == 0 {
			sender.Update(newConfig)
		} else {
			sender.Update(old)
		}
	}
	for i := 0; i < readers; i++ {
		<-done
	}
	sender.Update(newConfig)
	if sender.PublicURL() != newConfig.PublicURL || !sender.Configured() || !sender.VerificationReady() {
		t.Fatal("sender did not observe the final hot update")
	}
}

func TestTestSendCooldownIsSharedAndAtomic(t *testing.T) {
	ctx := context.Background()
	manager, err := NewManager(ctx, mailDatabase(t), New(Config{}), Config{}, []byte("test-instance-master-key"))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	now := time.UnixMilli(1_800_000_000_000)
	allowed, err := manager.ClaimTestSend(ctx, now)
	if err != nil || !allowed {
		t.Fatalf("first claim = %v, %v; want allowed", allowed, err)
	}
	allowed, err = manager.ClaimTestSend(ctx, now.Add(time.Minute))
	if err != nil || allowed {
		t.Fatalf("claim during cooldown = %v, %v; want denied", allowed, err)
	}
	allowed, err = manager.ClaimTestSend(ctx, now.Add(TestSendCooldown))
	if err != nil || !allowed {
		t.Fatalf("claim after cooldown = %v, %v; want allowed", allowed, err)
	}

	// Requests from concurrent administrators serialize through the same
	// database row; exactly one may claim the next window.
	start := make(chan struct{})
	var wg sync.WaitGroup
	var winners atomic.Int32
	errCh := make(chan error, 16)
	claimAt := now.Add(2 * TestSendCooldown)
	for i := 0; i < cap(errCh); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := manager.ClaimTestSend(ctx, claimAt)
			if err != nil {
				errCh <- err
				return
			}
			if claimed {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent claim: %v", err)
	}
	if got := winners.Load(); got != 1 {
		t.Fatalf("concurrent cooldown claims = %d, want exactly one", got)
	}
}

func TestMailConfigurationRejectsEnvelopeAndURLAmbiguity(t *testing.T) {
	base := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://example.com"}
	cases := []struct {
		name string
		edit func(*Config)
	}{
		{"missing sender", func(c *Config) { c.From = ""; c.Username = "smtp-login" }},
		{"display-name sender", func(c *Config) { c.From = "Sender <sender@example.com>" }},
		{"subpath public URL", func(c *Config) { c.PublicURL = "https://example.com/app" }},
		{"query public URL", func(c *Config) { c.PublicURL = "https://example.com/?next=/app" }},
		{"insecure public URL", func(c *Config) { c.PublicURL = "http://example.com" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.edit(&cfg)
			if err := ValidateConfig(cfg); err == nil {
				t.Fatal("ambiguous mail configuration was accepted")
			}
		})
	}
	if err := ValidateConfig(Config{}); err != nil {
		t.Fatalf("empty config should remain a valid disabled state: %v", err)
	}
}

// The public address is where mailed links point, so moving it is a super
// administrator's. Whoever may save the rest of the form may save the address
// as it already stands.
func TestOnlyASuperAdministratorMovesThePublicURL(t *testing.T) {
	ctx := context.Background()
	manager, err := NewManager(ctx, mailDatabase(t), New(Config{}), Config{}, []byte("test-instance-master-key"))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://arc.example.com"}
	if err := manager.Save(ctx, cfg, "the-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	moved := cfg
	moved.PublicURL = "https://phish.example.net"
	if err := manager.Save(ctx, moved, "", false, false); !errors.Is(err, ErrOriginNeedsSuperAdmin) {
		t.Fatalf("moving the public URL without authority = %v, want ErrOriginNeedsSuperAdmin", err)
	}
	if got, _ := manager.Config(); got.PublicURL != cfg.PublicURL {
		t.Fatalf("a refused move changed the public URL to %q", got.PublicURL)
	}

	// Everything else in the form, and the address as it stands, stay
	// available to a save that may not move it.
	edited := cfg
	edited.From = "notifications@example.com"
	if err := manager.Save(ctx, edited, "", false, false); err != nil {
		t.Fatalf("saving the form with the public URL unchanged: %v", err)
	}

	if err := manager.Save(ctx, moved, "", false, true); err != nil {
		t.Fatalf("super administrator moving the public URL: %v", err)
	}
	if got, _ := manager.Config(); got.PublicURL != moved.PublicURL {
		t.Fatalf("public URL = %q after the super administrator's move", got.PublicURL)
	}
}

// A manager that loaded before a move still compares against what is stored,
// so it cannot put the old address back by saving the form it remembers.
func TestAStaleManagerCannotRevertThePublicURL(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	master := []byte("test-instance-master-key")
	writer, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	cfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://old.example.com"}
	if err := writer.Save(ctx, cfg, "the-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	stale, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new stale manager: %v", err)
	}

	moved := cfg
	moved.PublicURL = "https://new.example.com"
	if err := writer.Save(ctx, moved, "", false, true); err != nil {
		t.Fatalf("move: %v", err)
	}

	back := cfg
	back.From = "edited@example.com"
	if err := stale.Save(ctx, back, "", false, false); !errors.Is(err, ErrOriginNeedsSuperAdmin) {
		t.Fatalf("stale manager put the old public URL back: %v", err)
	}
	reloaded, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, _ := reloaded.Config(); got.PublicURL != moved.PublicURL || got.From != cfg.From {
		t.Fatalf("stored configuration = %+v after a refused stale save", got)
	}
}

// The check and the write share one lock, so a save that may not move the
// public URL cannot land after a super administrator's move and undo it,
// whichever of the two reaches the database first.
func TestConcurrentPublicURLMoveIsNeverUndone(t *testing.T) {
	ctx := context.Background()
	db := mailDatabase(t)
	master := []byte("test-instance-master-key")
	admin, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new administrator manager: %v", err)
	}
	oldCfg := Config{Host: "smtp.example.com", Port: 587, From: "sender@example.com", PublicURL: "https://old.example.com"}
	if err := admin.Save(ctx, oldCfg, "the-password", false, true); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	stale, err := NewManager(ctx, db, New(Config{}), Config{}, master)
	if err != nil {
		t.Fatalf("new stale manager: %v", err)
	}
	newCfg := oldCfg
	newCfg.PublicURL = "https://new.example.com"

	for round := 0; round < 25; round++ {
		if err := admin.Save(ctx, oldCfg, "", false, true); err != nil {
			t.Fatalf("round %d: put the old address back: %v", round, err)
		}

		start := make(chan struct{})
		var (
			wg                sync.WaitGroup
			moveErr, staleErr error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			moveErr = admin.Save(ctx, newCfg, "", false, true)
		}()
		go func() {
			defer wg.Done()
			<-start
			staleErr = stale.Save(ctx, oldCfg, "", false, false)
		}()
		close(start)
		wg.Wait()

		if moveErr != nil {
			t.Fatalf("round %d: super administrator's move: %v", round, moveErr)
		}
		if staleErr != nil && !errors.Is(staleErr, ErrOriginNeedsSuperAdmin) {
			t.Fatalf("round %d: stale save: %v", round, staleErr)
		}
		reloaded, err := NewManager(ctx, db, New(Config{}), Config{}, master)
		if err != nil {
			t.Fatalf("round %d: reload: %v", round, err)
		}
		if got, _ := reloaded.Config(); got.PublicURL != newCfg.PublicURL {
			t.Fatalf("round %d: the move was undone; public URL = %q", round, got.PublicURL)
		}
	}
}
