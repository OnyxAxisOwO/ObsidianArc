package config

import (
	"log/slog"
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
	if cfg.TrustCloudflare {
		t.Error("CF-Connecting-IP is believed without the operator's claim")
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

// A key file that exists without a usable key stops the start, and the file is
// left exactly as it was. Overwriting it would make every value sealed under
// the original key unreadable, with nothing at boot to say why.
func TestAnUnusableSecretFileRefusesToStartAndIsLeftAlone(t *testing.T) {
	for name, contents := range map[string]string{
		"empty":               "",
		"whitespace only":     " \n\t\n",
		"short":               "too-short\n",
		"one under the floor": strings.Repeat("k", minSecretLen-1) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, SecretKeyFile)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(envPrefix+"DATA_DIR", dir)
			t.Setenv(envPrefix+"SECRET_KEY", "")

			_, err := Load()
			if err == nil {
				t.Fatal("a key file with no usable key let the instance start")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the error does not name the file: %v", err)
			}
			if !strings.Contains(err.Error(), "Restore") || !strings.Contains(err.Error(), "Delete") {
				t.Errorf("the error does not say what to do: %v", err)
			}

			left, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(left) != contents {
				t.Errorf("the key file was rewritten from %q to %q", contents, left)
			}
		})
	}
}

// The first start on an empty data directory creates the key file, and the key
// the instance then uses is the one stored in it.
func TestAMissingSecretFileIsCreatedWithTheKeyInUse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envPrefix+"DATA_DIR", dir)
	t.Setenv(envPrefix+"SECRET_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, SecretKeyFile))
	if err != nil {
		t.Fatalf("no key file was written: %v", err)
	}
	if strings.TrimSpace(string(stored)) != string(cfg.SecretKey) {
		t.Error("the key file does not hold the key the instance uses")
	}
}

// A key file that holds a usable key is loaded as it is, and nothing is written
// back to it. The surrounding whitespace belongs to the editor, not the key.
func TestAValidSecretFileIsLoadedAndNotRewritten(t *testing.T) {
	const key = "7f3c9a1e5b2d48608c7e1a4f9b3d2c5e8a6f01"
	dir := t.TempDir()
	path := filepath.Join(dir, SecretKeyFile)
	contents := "  " + key + "\r\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envPrefix+"DATA_DIR", dir)
	t.Setenv(envPrefix+"SECRET_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg.SecretKey) != key {
		t.Error("the instance did not use the key stored in the file")
	}
	// A key read back from the data directory is still reported as coming from
	// it: main advises OBSIDIAN_SECRET_KEY on every start for that reason.
	if !cfg.GeneratedSecret() {
		t.Error("a key read from the data directory was not reported as coming from it")
	}
	left, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != contents {
		t.Errorf("the key file was rewritten from %q to %q", contents, left)
	}
}

// The environment key takes precedence and never reads the file, so a stale or
// truncated file in the data directory cannot stop a deployment that supplies
// its key through the environment.
func TestTheEnvironmentKeyIgnoresAnUnusableSecretFile(t *testing.T) {
	const key = "7f3c9a1e5b2d48608c7e1a4f9b3d2c5e8a6f01"
	dir := t.TempDir()
	path := filepath.Join(dir, SecretKeyFile)
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envPrefix+"DATA_DIR", dir)
	t.Setenv(envPrefix+"SECRET_KEY", key)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("a stale key file blocked a start that supplies its key: %v", err)
	}
	if string(cfg.SecretKey) != key {
		t.Error("the environment key was not used")
	}
	left, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != "short\n" {
		t.Errorf("the key file was rewritten to %q", left)
	}
}

// Starts that find no key file race to create one. Before the create was
// exclusive, each could write its own key over the other's, so one instance
// served a key that was already gone from disk. Every start that succeeds must
// return the key the file ends up holding, and that is what each trial checks.
// Real goroutines, released together, because the bug is the window between
// the read and the write.
func TestConcurrentFirstStartsAgreeOnOneKey(t *testing.T) {
	t.Setenv(envPrefix+"SECRET_KEY", "")
	const trials = 200
	const starters = 8

	type outcome struct {
		key []byte
		err error
	}
	for trial := 0; trial < trials; trial++ {
		dir := t.TempDir()
		gate := make(chan struct{})
		results := make(chan outcome, starters)
		for i := 0; i < starters; i++ {
			go func() {
				<-gate
				key, _, err := loadOrCreateSecret(dir)
				results <- outcome{key: key, err: err}
			}()
		}
		close(gate)

		// Every result is collected before anything can fail the trial, so no
		// start is still touching the directory when the test stops.
		outcomes := make([]outcome, 0, starters)
		for i := 0; i < starters; i++ {
			outcomes = append(outcomes, <-results)
		}
		var agreed []byte
		succeeded := 0
		for _, r := range outcomes {
			if r.err != nil {
				continue
			}
			succeeded++
			if agreed == nil {
				agreed = r.key
			} else if string(r.key) != string(agreed) {
				t.Fatalf("trial %d: two first starts used different keys", trial)
			}
		}
		if succeeded == 0 {
			t.Fatalf("trial %d: no first start got a key", trial)
		}
		stored, err := os.ReadFile(filepath.Join(dir, SecretKeyFile))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(stored)) != string(agreed) {
			t.Fatalf("trial %d: the key file does not hold the key the starts used", trial)
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

// A short or guessable key still boots — refusing would take down deployments
// that already run on one — but the operator is told, once, at startup.
func TestAWeakSuppliedSecretStillStartsButIsWarnedAbout(t *testing.T) {
	var logged strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	weak := map[string]string{
		"sixteen chars!!!":                       "only 16 characters",
		strings.Repeat("a", 40):                  "one kind of character",
		"0123456789012345678901234567890123456":  "one kind of character",
		strings.Repeat("ab12", 10):               "distinct characters",
		"passwordpasswordpasswordpasswordpass":   "one kind of character",
		strings.Repeat("Ab", 20) + "0123456789":  "",
		"7f3c9a1e5b2d48608c7e1a4f9b3d2c5e8a6f01": "",
	}
	for key, want := range weak {
		logged.Reset()
		cfg := load(t, map[string]string{"SECRET_KEY": key})
		if string(cfg.SecretKey) != key {
			t.Fatalf("%q: the supplied key was not used", key)
		}
		if want == "" {
			if strings.Contains(logged.String(), "weak") {
				t.Errorf("%q: a fine key was warned about: %s", key, logged.String())
			}
			continue
		}
		if !strings.Contains(logged.String(), "weak") || !strings.Contains(logged.String(), "OBSIDIAN_SECRET_KEY") ||
			!strings.Contains(logged.String(), want) {
			t.Errorf("%q: no warning naming the setting and %q:\n%s", key, want, logged.String())
		}
		if strings.Contains(logged.String(), key) {
			t.Errorf("%q: the warning printed the key itself", key)
		}
	}
}

func TestAGeneratedSecretIsNotWarnedAbout(t *testing.T) {
	var logged strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	load(t, nil)
	if strings.Contains(logged.String(), "weak") {
		t.Errorf("a key this process generated was called weak:\n%s", logged.String())
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

func TestTrustCloudflareParsesOn(t *testing.T) {
	if !load(t, map[string]string{"TRUST_CLOUDFLARE": "true"}).TrustCloudflare {
		t.Error("TRUST_CLOUDFLARE=true was not read as the claim")
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
		"TRUST_CLOUDFLARE": "maybe",
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
	if cfg.TrustCloudflare {
		t.Error("an unparseable TRUST_CLOUDFLARE was read as true")
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
