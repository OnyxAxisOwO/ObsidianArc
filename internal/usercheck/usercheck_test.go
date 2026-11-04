package usercheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func usercheckDatabase(t *testing.T) *database.DB {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "usercheck.db"),
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

func clientFor(t *testing.T, fn roundTripFunc) *http.Client {
	t.Helper()
	return &http.Client{Transport: fn}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestPaidLookupRejectsDisposableAndSkipsExemptDomains(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	client := clientFor(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Host != "api.usercheck.com" || r.URL.Scheme != "https" {
			t.Errorf("unexpected UserCheck request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer paid-key" {
			t.Error("paid API key was not sent as a Bearer token")
		}
		if r.URL.Query().Get("include_mx") != "false" {
			t.Error("unused MX lookup was not disabled")
		}
		return response(http.StatusOK, `{"disposable":true}`), nil
	})
	m, err := NewManager(ctx, usercheckDatabase(t), []byte("master-key"), client)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Check(ctx, "test@new-domain.example"); err != nil {
		t.Fatalf("disabled check called provider: %v", err)
	}
	if err := m.Save(ctx, Config{Enabled: true, ExemptDomains: []string{"GMAIL.COM", "gmail.com"}, FailureMode: FailureReject}, "paid-key", false); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := m.Config().ExemptDomains; len(got) != 1 || got[0] != "gmail.com" {
		t.Fatalf("normalized exemptions = %v", got)
	}
	if err := m.Check(ctx, "someone@GMAIL.COM"); err != nil {
		t.Fatalf("exempt domain was rejected: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("exempt domain used %d paid lookups", got)
	}
	if err := m.Check(ctx, "someone@not-gmail.com"); !errors.Is(err, ErrDisposable) {
		t.Fatalf("uncommon disposable email = %v, want ErrDisposable", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("lookup count = %d, want 1", got)
	}
	result, err := m.Test(ctx, "someone@gmail.com")
	if err != nil || !result.Disposable || result.Skipped {
		t.Fatalf("test must force a real lookup on exempt domain: %+v, %v", result, err)
	}
}

func TestEmptyExemptListStaysAnEmptyListForTheBackoffice(t *testing.T) {
	ctx := context.Background()
	db := usercheckDatabase(t)
	m, err := NewManager(ctx, db, []byte("master-key"), nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Save(ctx, Config{Enabled: false, ExemptDomains: []string{}, FailureMode: FailureReject}, "", false); err != nil {
		t.Fatalf("save empty list: %v", err)
	}
	if got := m.Config().ExemptDomains; got == nil {
		t.Fatal("live configuration returned a nil list; it marshals as null")
	}
	reloaded, err := NewManager(ctx, db, []byte("master-key"), nil)
	if err != nil {
		t.Fatalf("reload manager: %v", err)
	}
	if got := reloaded.Config().ExemptDomains; got == nil || len(got) != 0 {
		t.Fatalf("reloaded exemptions = %#v, want an empty non-nil list", got)
	}
}

func TestAPIKeyIsSealedAndBlankSavePreservesIt(t *testing.T) {
	ctx := context.Background()
	db := usercheckDatabase(t)
	key := "paid-api-key-that-must-stay-private"
	m, err := NewManager(ctx, db, []byte("master-key"), nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := Config{Enabled: true, ExemptDomains: []string{}, FailureMode: FailureReject}
	if err := m.Save(ctx, cfg, key, false); err != nil {
		t.Fatalf("save API key: %v", err)
	}
	var sealed []byte
	if err := db.QueryRow(ctx, `SELECT api_key FROM usercheck_config WHERE id = 1`).Scan(&sealed); err != nil {
		t.Fatalf("read sealed key: %v", err)
	}
	if bytes.Contains(sealed, []byte(key)) {
		t.Fatal("API key was stored in plaintext")
	}
	if err := m.Save(ctx, cfg, "", false); err != nil {
		t.Fatalf("save without key: %v", err)
	}
	reloaded, err := NewManager(ctx, db, []byte("master-key"), nil)
	if err != nil || !reloaded.Config().APIKeySet {
		t.Fatalf("reload lost key: %v", err)
	}
	if err := reloaded.Save(ctx, Config{FailureMode: FailureReject}, "", true); err != nil {
		t.Fatalf("explicitly clear key: %v", err)
	}
	if reloaded.Config().APIKeySet {
		t.Fatal("clear API key left it configured")
	}
	if err := reloaded.Save(ctx, cfg, "", false); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("enabled without a key = %v", err)
	}
}

func TestStaleManagerPreservesLatestAPIKeyAcrossSaves(t *testing.T) {
	ctx := context.Background()
	db := usercheckDatabase(t)
	master := []byte("master-key")
	first, err := NewManager(ctx, db, master, nil)
	if err != nil {
		t.Fatalf("new first manager: %v", err)
	}
	stale, err := NewManager(ctx, db, master, nil)
	if err != nil {
		t.Fatalf("new stale manager: %v", err)
	}
	cfg := Config{Enabled: true, ExemptDomains: []string{}, FailureMode: FailureReject}
	if err := first.Save(ctx, cfg, "initial-key", false); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if err := first.Save(ctx, cfg, "rotated-key", false); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	if err := stale.Save(ctx, cfg, "", false); err != nil {
		t.Fatalf("stale manager save: %v", err)
	}
	if got := readStoredUserCheckKey(t, db, master); got != "rotated-key" {
		t.Fatalf("stale manager restored key %q, want rotated key", got)
	}
	if !stale.Config().APIKeySet {
		t.Fatal("stale manager did not adopt the rotated key")
	}

	disabled := Config{Enabled: false, ExemptDomains: []string{}, FailureMode: FailureReject}
	if err := first.Save(ctx, disabled, "", true); err != nil {
		t.Fatalf("clear API key: %v", err)
	}
	if err := stale.Save(ctx, disabled, "", false); err != nil {
		t.Fatalf("save after clear: %v", err)
	}
	if got := readStoredUserCheckKey(t, db, master); got != "" {
		t.Fatalf("stale manager restored a cleared API key %q", got)
	}
	if stale.Config().APIKeySet {
		t.Fatal("stale manager reports a key after the database key was cleared")
	}
}

func TestConcurrentManagerSavesKeepRotatedAPIKey(t *testing.T) {
	ctx := context.Background()
	db := usercheckDatabase(t)
	master := []byte("master-key")
	first, err := NewManager(ctx, db, master, nil)
	if err != nil {
		t.Fatalf("new first manager: %v", err)
	}
	cfg := Config{Enabled: true, ExemptDomains: []string{}, FailureMode: FailureReject}
	if err := first.Save(ctx, cfg, "initial-key", false); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	stale, err := NewManager(ctx, db, master, nil)
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
		errs <- first.Save(ctx, cfg, "rotated-key", false)
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- stale.Save(ctx, cfg, "", false)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save: %v", err)
		}
	}
	if got := readStoredUserCheckKey(t, db, master); got != "rotated-key" {
		t.Fatalf("concurrent saves left key %q, want rotated key", got)
	}
}

// A key sealed under a master secret this process no longer has is what a
// key-lost restore leaves behind. The server must still boot, the settings
// screen must still be able to enter or clear the key, and screening stays on
// so the failure policy decides, rather than being switched off silently.
func TestUnreadableStoredKeyDoesNotBlockBootOrRecovery(t *testing.T) {
	ctx := context.Background()
	db := usercheckDatabase(t)
	cfg := Config{Enabled: true, ExemptDomains: []string{}, FailureMode: FailureReject}
	first, err := NewManager(ctx, db, []byte("master-a"), nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := first.Save(ctx, cfg, "sealed-under-a", false); err != nil {
		t.Fatalf("save key: %v", err)
	}

	recovered, err := NewManager(ctx, db, []byte("master-b"), nil)
	if err != nil {
		t.Fatalf("boot with an unreadable key: %v", err)
	}
	if recovered.Config().APIKeySet || !recovered.Config().Enabled {
		t.Fatalf("unreadable key config = %+v, want enabled without a key", recovered.Config())
	}
	if err := recovered.Check(ctx, "person@other.example"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("enabled without a readable key in reject mode = %v", err)
	}
	if err := recovered.Save(ctx, cfg, "", false); !errors.Is(err, ErrKeyUnreadable) || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("blank save over an unreadable key = %v, want ErrKeyUnreadable", err)
	}
	if err := recovered.Save(ctx, cfg, "entered-again", false); err != nil {
		t.Fatalf("entering a new key over an unreadable one: %v", err)
	}
	if got := readStoredUserCheckKey(t, db, []byte("master-b")); got != "entered-again" {
		t.Fatalf("stored key = %q, want the entered key", got)
	}
	reloaded, err := NewManager(ctx, db, []byte("master-b"), nil)
	if err != nil || !reloaded.Config().APIKeySet {
		t.Fatalf("reload after repair: %v, %+v", err, reloaded.Config())
	}

	// Clearing must not need to read the row either.
	cleared, err := NewManager(ctx, db, []byte("master-c"), nil)
	if err != nil {
		t.Fatalf("boot under a third secret: %v", err)
	}
	disabled := Config{Enabled: false, ExemptDomains: []string{}, FailureMode: FailureReject}
	if err := cleared.Save(ctx, disabled, "", true); err != nil {
		t.Fatalf("clear over an unreadable key: %v", err)
	}
	if got := readStoredUserCheckKey(t, db, []byte("master-b")); got != "" {
		t.Fatalf("cleared key = %q", got)
	}
}

// Switching screening off needs no key, so it must not be refused over a key
// this process cannot open: the operator would otherwise have to tick clear
// just to turn the check off.
func TestSwitchingOffOverUnreadableStoredKeySucceeds(t *testing.T) {
	ctx := context.Background()
	db := usercheckDatabase(t)
	cfg := Config{Enabled: true, ExemptDomains: []string{}, FailureMode: FailureReject}
	first, err := NewManager(ctx, db, []byte("master-a"), nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := first.Save(ctx, cfg, "sealed-under-a", false); err != nil {
		t.Fatalf("save key: %v", err)
	}

	recovered, err := NewManager(ctx, db, []byte("master-b"), nil)
	if err != nil {
		t.Fatalf("boot with an unreadable key: %v", err)
	}
	off := Config{Enabled: false, ExemptDomains: []string{"example.com"}, FailureMode: FailureAllow}
	if err := recovered.Save(ctx, off, "", false); err != nil {
		t.Fatalf("switching off over an unreadable key: %v", err)
	}
	if got := recovered.Config(); got.Enabled || got.APIKeySet || got.FailureMode != FailureAllow {
		t.Fatalf("config after switching off = %+v", got)
	}
	if got := readStoredUserCheckKey(t, db, []byte("master-b")); got != "" {
		t.Fatalf("unreadable key was kept while screening is off: %q", got)
	}
	reloaded, err := NewManager(ctx, db, []byte("master-b"), nil)
	if err != nil || reloaded.Config().Enabled || len(reloaded.Config().ExemptDomains) != 1 {
		t.Fatalf("reload after switching off: %v, %+v", err, reloaded.Config())
	}
}

func readStoredUserCheckKey(t *testing.T, db *database.DB, master []byte) string {
	t.Helper()
	var sealed []byte
	if err := db.QueryRow(context.Background(), `SELECT api_key FROM usercheck_config WHERE id = 1`).Scan(&sealed); err != nil {
		t.Fatalf("read stored key: %v", err)
	}
	if len(sealed) == 0 {
		return ""
	}
	box, err := secret.New(master, "obsidian-arc/usercheck-api-key")
	if err != nil {
		t.Fatalf("create API key box: %v", err)
	}
	key, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("open stored key: %v", err)
	}
	return key
}

func TestFailurePolicyIsAppliedToUnavailableAndMalformedResponses(t *testing.T) {
	ctx := context.Background()
	var status atomic.Int32
	var calls atomic.Int32
	status.Store(http.StatusTooManyRequests)
	client := clientFor(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		if status.Load() == http.StatusOK {
			return response(http.StatusOK, `{}`), nil
		}
		return response(int(status.Load()), `{"error":"Too many requests"}`), nil
	})
	m, err := NewManager(ctx, usercheckDatabase(t), []byte("master-key"), client)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := Config{Enabled: true, FailureMode: FailureReject}
	if err := m.Save(ctx, cfg, "paid-key", false); err != nil {
		t.Fatalf("save reject mode: %v", err)
	}
	if err := m.Check(ctx, "person@other.example"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("429 in reject mode = %v", err)
	}
	if err := m.Check(ctx, "other@other.example"); !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("429 did not pause later paid lookups: %v, calls %d", err, calls.Load())
	}
	status.Store(http.StatusOK)
	if err := m.Save(ctx, cfg, "replacement-paid-key", false); err != nil {
		t.Fatalf("replace key to clear provider cooldown: %v", err)
	}
	if err := m.Check(ctx, "person@other.example"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing disposable field in reject mode = %v", err)
	}
	cfg.FailureMode = FailureAllow
	if err := m.Save(ctx, cfg, "", false); err != nil {
		t.Fatalf("save allow mode: %v", err)
	}
	if err := m.Check(ctx, "person@other.example"); err != nil {
		t.Fatalf("allow mode did not continue on provider failure: %v", err)
	}
}

// A wrong or revoked key is an operator's mistake to fix, not an outage to
// wait out, so it must not be reported as the same error a 503 is.
func TestRefusedKeyIsNotReportedAsAnOutage(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		client := clientFor(t, func(*http.Request) (*http.Response, error) {
			return response(status, `{"error":"Unauthorized"}`), nil
		})
		m, err := NewManager(ctx, usercheckDatabase(t), []byte("master-key"), client)
		if err != nil {
			t.Fatalf("new manager: %v", err)
		}
		if err := m.Save(ctx, Config{Enabled: true, FailureMode: FailureReject}, "revoked-key", false); err != nil {
			t.Fatalf("save key: %v", err)
		}
		if _, err := m.Test(ctx, "someone@other.example"); !errors.Is(err, ErrKeyRefused) || errors.Is(err, ErrUnavailable) {
			t.Fatalf("HTTP %d from the provider = %v, want ErrKeyRefused alone", status, err)
		}
	}
}

func TestReleasingATestClaimOnlyResetsTheClaimItMade(t *testing.T) {
	ctx := context.Background()
	m, err := NewManager(ctx, usercheckDatabase(t), []byte("master-key"), nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	now := time.UnixMilli(1_800_000_000_000)
	if allowed, err := m.ClaimTest(ctx, now); err != nil || !allowed {
		t.Fatalf("first claim = %v, %v", allowed, err)
	}
	if err := m.ReleaseTest(ctx, now.Add(time.Second)); err != nil {
		t.Fatalf("release of another claim: %v", err)
	}
	if allowed, _ := m.ClaimTest(ctx, now.Add(time.Minute)); allowed {
		t.Fatal("releasing a claim it did not make reopened the cooldown")
	}
	if err := m.ReleaseTest(ctx, now); err != nil {
		t.Fatalf("release: %v", err)
	}
	if allowed, err := m.ClaimTest(ctx, now.Add(time.Minute)); err != nil || !allowed {
		t.Fatalf("claim after release = %v, %v", allowed, err)
	}
}

func TestPaidTestCooldownHasOneWinnerAcrossConcurrentCalls(t *testing.T) {
	ctx := context.Background()
	m, err := NewManager(ctx, usercheckDatabase(t), []byte("master-key"), nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	now := time.UnixMilli(1_800_000_000_000)
	allowed, err := m.ClaimTest(ctx, now)
	if err != nil || !allowed {
		t.Fatalf("first claim = %v, %v", allowed, err)
	}
	allowed, err = m.ClaimTest(ctx, now.Add(time.Minute))
	if err != nil || allowed {
		t.Fatalf("claim during cooldown = %v, %v", allowed, err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var winners atomic.Int32
	errCh := make(chan error, 12)
	for i := 0; i < cap(errCh); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := m.ClaimTest(ctx, now.Add(TestCooldown))
			if err != nil {
				errCh <- err
			} else if claimed {
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
	if winners.Load() != 1 {
		t.Fatalf("concurrent winners = %d, want 1", winners.Load())
	}
}
