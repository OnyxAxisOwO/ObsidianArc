package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Load reads the environment, so every test here sets it explicitly and lets
// t.Setenv put it back. The data directory is a fresh one each time: Load
// creates it, and writes a secret key into it when none was supplied.
func load(t *testing.T, environment map[string]string) Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(envPrefix+"DATA_DIR", dir)
	for key, value := range environment {
		t.Setenv(envPrefix+key, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func TestDefaultsAreTheDocumentedOnes(t *testing.T) {
	cfg := load(t, nil)

	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("Driver = %q", cfg.Database.Driver)
	}
	if filepath.Base(cfg.Database.DSN) != "obsidian.db" {
		t.Errorf("DSN = %q, want a file in the data directory", cfg.Database.DSN)
	}
	if cfg.TrustProxy {
		t.Error("forwarded headers are trusted by default")
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want empty", cfg.TrustedProxies)
	}
	if !cfg.Session.SecureCookie {
		t.Error("the session cookie is not marked secure by default")
	}
	if cfg.Session.TTL != 30*24*time.Hour {
		t.Errorf("TTL = %s", cfg.Session.TTL)
	}
	if cfg.Mail.ImplicitTLS {
		t.Error("implicit TLS is on for the default port 587")
	}
}

// The one coupling in here that is a security decision rather than a
// convenience: a development run drops the Secure flag so a plain-http
// localhost session works, and nothing else may.
func TestSecureCookieFollowsDevUnlessSaidOtherwise(t *testing.T) {
	if load(t, map[string]string{"DEV": "true"}).Session.SecureCookie {
		t.Error("a dev run kept the Secure flag, so a plain-http session cannot work")
	}
	if !load(t, map[string]string{"DEV": "true", "COOKIE_SECURE": "true"}).Session.SecureCookie {
		t.Error("an explicit COOKIE_SECURE was overridden by DEV")
	}
	if load(t, map[string]string{"COOKIE_SECURE": "false"}).Session.SecureCookie {
		t.Error("COOKIE_SECURE=false was ignored outside dev")
	}
}

// This list decides who may claim to be somebody else. A stray space or a
// trailing comma must not turn into an entry that matches nothing, or into
// one that matches everything.
func TestTrustedProxiesParsing(t *testing.T) {
	for raw, want := range map[string][]string{
		"10.0.0.1":                    {"10.0.0.1"},
		" 10.0.0.1 , 10.0.0.2 ":       {"10.0.0.1", "10.0.0.2"},
		"10.0.0.0/8,,192.168.0.0/16,": {"10.0.0.0/8", "192.168.0.0/16"},
		",":                           nil,
		" ":                           nil,
		"":                            nil,
	} {
		got := load(t, map[string]string{"TRUSTED_PROXIES": raw}).TrustedProxies
		if len(got) != len(want) {
			t.Errorf("%q -> %v, want %v", raw, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q -> %v, want %v", raw, got, want)
				break
			}
		}
	}
}

func TestAnUnknownDriverIsRefusedRatherThanAssumed(t *testing.T) {
	t.Setenv(envPrefix+"DATA_DIR", t.TempDir())
	t.Setenv(envPrefix+"DB_DRIVER", "mysql")
	if _, err := Load(); err == nil {
		t.Fatal("an unknown driver was accepted")
	}
}

func TestDriverSpellingsAreNormalized(t *testing.T) {
	for raw, want := range map[string]string{
		"sqlite": "sqlite", "sqlite3": "sqlite", "SQLite": "sqlite",
		"postgres": "postgres", "postgresql": "postgres", "pgx": "postgres",
	} {
		environment := map[string]string{"DB_DRIVER": raw}
		if want == "postgres" {
			environment["DB_DSN"] = "postgres://localhost/arc"
		}
		if got := load(t, environment).Database.Driver; got != want {
			t.Errorf("%q -> %q, want %q", raw, got, want)
		}
	}
}

// Postgres has nowhere to default to. Falling back to a file would start a
// server that looks healthy and holds none of the operator's data.
func TestPostgresWithoutADSNIsRefused(t *testing.T) {
	t.Setenv(envPrefix+"DATA_DIR", t.TempDir())
	t.Setenv(envPrefix+"DB_DRIVER", "postgres")
	t.Setenv(envPrefix+"DB_DSN", "")
	if _, err := Load(); err == nil {
		t.Fatal("postgres was accepted with no DSN")
	}
}

func TestSecretKeyIsGeneratedOnceAndThenReused(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envPrefix+"DATA_DIR", dir)
	t.Setenv(envPrefix+"SECRET_KEY", "")

	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !first.GeneratedSecret() {
		t.Error("the first run did not report that it generated a key")
	}
	if len(first.SecretKey) < 16 {
		t.Fatalf("generated a %d-byte key", len(first.SecretKey))
	}

	second, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(second.SecretKey) != string(first.SecretKey) {
		t.Fatal("the second run generated a different key; every provider key on disk would be unreadable")
	}

	info, err := os.Stat(filepath.Join(dir, SecretKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	// Only where the bits mean something. NTFS does not map them, and Go
	// reports 0666 for any writable file on Windows, so asserting there would
	// fail on a platform that never had the problem. CI runs Linux, which is
	// also where this instance's key will actually be sitting on a disk.
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("the key file is mode %o, readable by somebody other than its owner", mode)
		}
	}
}

func TestASuppliedSecretIsUsedAndBoundedBelow(t *testing.T) {
	cfg := load(t, map[string]string{"SECRET_KEY": strings.Repeat("k", 32)})
	if cfg.GeneratedSecret() {
		t.Error("a supplied key was reported as generated")
	}
	if string(cfg.SecretKey) != strings.Repeat("k", 32) {
		t.Error("the supplied key was not the one used")
	}

	t.Setenv(envPrefix+"DATA_DIR", t.TempDir())
	t.Setenv(envPrefix+"SECRET_KEY", "too-short")
	if _, err := Load(); err == nil {
		t.Fatal("a nine-character secret key was accepted")
	}
}

// 465 is implicit TLS everywhere it is offered, and it is the setting
// operators most often leave out.
func TestImplicitTLSFollowsThePort(t *testing.T) {
	if !load(t, map[string]string{"SMTP_PORT": "465"}).Mail.ImplicitTLS {
		t.Error("port 465 did not turn on implicit TLS")
	}
	if load(t, map[string]string{"SMTP_PORT": "465", "SMTP_TLS": "false"}).Mail.ImplicitTLS {
		t.Error("an explicit SMTP_TLS=false was overridden by the port")
	}
}

// A value that will not parse falls back to the default rather than refusing
// to boot. That is a deliberate choice — a typo in a tuning knob should not
// take the server down — and it means a typo is silent, so it is worth
// stating rather than discovering.
func TestUnparseableValuesFallBackToTheDefault(t *testing.T) {
	cfg := load(t, map[string]string{
		"SESSION_TTL":      "not-a-duration",
		"DB_MAX_CONNS":     "several",
		"TRUST_PROXY":      "maybe",
		"SMTP_PORT":        "",
		"ARGON_MEMORY_KIB": "lots",
	})
	if cfg.Session.TTL != 30*24*time.Hour {
		t.Errorf("TTL = %s, want the default", cfg.Session.TTL)
	}
	if cfg.Database.MaxOpenConns != 4 {
		t.Errorf("MaxOpenConns = %d, want the sqlite default", cfg.Database.MaxOpenConns)
	}
	if cfg.TrustProxy {
		t.Error("an unparseable TRUST_PROXY was read as true")
	}
	if cfg.Mail.Port != 587 {
		t.Errorf("Port = %d, want 587", cfg.Mail.Port)
	}
	if cfg.Password.Memory != 19456 {
		t.Errorf("Argon memory = %d, want the default", cfg.Password.Memory)
	}
}

func TestPoolAndHasherAreKeptSelfConsistent(t *testing.T) {
	cfg := load(t, map[string]string{"DB_MAX_CONNS": "2", "DB_MAX_IDLE_CONNS": "9"})
	if cfg.Database.MaxIdleConns > cfg.Database.MaxOpenConns {
		t.Errorf("idle %d exceeds open %d", cfg.Database.MaxIdleConns, cfg.Database.MaxOpenConns)
	}

	if got := load(t, map[string]string{"ARGON_MAX_PARALLEL": "0"}).Password.MaxParallel; got < 1 {
		t.Errorf("MaxParallel = %d; zero would let no password ever be hashed", got)
	}
}

func TestPostgresGetsALargerPoolThanSQLite(t *testing.T) {
	sqlite := load(t, nil).Database.MaxOpenConns
	postgres := load(t, map[string]string{
		"DB_DRIVER": "postgres", "DB_DSN": "postgres://localhost/arc",
	}).Database.MaxOpenConns
	if postgres <= sqlite {
		t.Errorf("postgres pool %d is not larger than sqlite's %d", postgres, sqlite)
	}
}
