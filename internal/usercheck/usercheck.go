// Package usercheck rejects disposable addresses when an operator enables
// paid UserCheck lookups for email domains outside the local exemption list.
package usercheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

const (
	FailureAllow  = "allow"
	FailureReject = "reject"
	TestCooldown  = 5 * time.Minute
	apiURL        = "https://api.usercheck.com/email/"
)

var (
	ErrDisposable    = errors.New("usercheck: disposable email addresses are not allowed")
	ErrUnavailable   = errors.New("usercheck: email screening is temporarily unavailable")
	ErrNotConfigured = errors.New("usercheck: an API key is required when screening is enabled")
	ErrInvalidConfig = errors.New("usercheck: invalid configuration")
	domainRE         = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
)

// The default list is intentionally small and editable. Every listed domain
// bypasses the API entirely, including address-level signals on that domain.
var DefaultExemptDomains = []string{
	"gmail.com", "outlook.com", "hotmail.com", "qq.com", "163.com",
	"126.com", "icloud.com", "yahoo.com", "foxmail.com", "proton.me",
}

// Config is safe to return to a browser. The API key is held separately.
type Config struct {
	Enabled       bool     `json:"enabled"`
	ExemptDomains []string `json:"exempt_domains"`
	FailureMode   string   `json:"failure_mode"`
	APIKeySet     bool     `json:"api_key_set"`
}

type Result struct {
	Disposable bool `json:"disposable"`
	Skipped    bool `json:"skipped"`
}

type Manager struct {
	mu     sync.RWMutex
	db     *database.DB
	box    *secret.Box
	client *http.Client
	cfg    Config
	apiKey string
	// A 429 must pause subsequent lookups. Repeating it can trigger a
	// temporary IP block at UserCheck and keep the provider unavailable.
	backoffUntil time.Time
}

func NewManager(ctx context.Context, db *database.DB, masterKey []byte, client *http.Client) (*Manager, error) {
	box, err := secret.New(masterKey, "obsidian-arc/usercheck-api-key")
	if err != nil {
		return nil, fmt.Errorf("usercheck: create API key box: %w", err)
	}
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	m := &Manager{
		db: db, box: box, client: client,
		cfg: Config{ExemptDomains: append([]string(nil), DefaultExemptDomains...), FailureMode: FailureReject},
	}
	if err := m.load(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) load(ctx context.Context) error {
	var cfg Config
	var domains string
	var sealed []byte
	err := m.db.QueryRow(ctx, `SELECT enabled, exempt_domains, failure_mode, api_key FROM usercheck_config WHERE id = 1`).
		Scan(&cfg.Enabled, &domains, &cfg.FailureMode, &sealed)
	if database.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("usercheck: load configuration: %w", err)
	}
	if err := json.Unmarshal([]byte(domains), &cfg.ExemptDomains); err != nil {
		return fmt.Errorf("usercheck: decode exempt domains: %w", err)
	}
	if _, err := normalizeConfig(cfg); err != nil {
		return fmt.Errorf("usercheck: invalid stored configuration: %w", err)
	}
	if len(sealed) > 0 {
		m.apiKey, err = m.box.Open(sealed)
		if err != nil {
			return fmt.Errorf("usercheck: open stored API key: %w", err)
		}
	}
	if cfg.Enabled && m.apiKey == "" {
		return ErrNotConfigured
	}
	cfg.APIKeySet = m.apiKey != ""
	m.cfg = cfg
	return nil
}

func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := m.cfg
	cfg.ExemptDomains = append([]string(nil), cfg.ExemptDomains...)
	return cfg
}

// Save first stores the complete configuration and then swaps the live copy.
// An empty apiKey preserves the existing key unless clearKey is explicit.
func (m *Manager) Save(ctx context.Context, cfg Config, apiKey string, clearKey bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	cfg, err = normalizeConfig(cfg)
	if err != nil {
		return err
	}
	domains, err := json.Marshal(cfg.ExemptDomains)
	if err != nil {
		return fmt.Errorf("usercheck: encode exempt domains: %w", err)
	}
	key := ""
	if err := m.db.Tx(ctx, func(tx *database.Tx) error {
		if err := settings.Lock(ctx, tx); err != nil {
			return err
		}

		// A stale replica must preserve the key that is current when its save
		// reaches the shared row lock, not the key cached when it started.
		var currentSealed []byte
		readErr := tx.QueryRow(ctx, `SELECT api_key FROM usercheck_config WHERE id = 1`).Scan(&currentSealed)
		currentKey := ""
		switch {
		case readErr == nil && len(currentSealed) > 0:
			opened, err := m.box.Open(currentSealed)
			if err != nil {
				return fmt.Errorf("usercheck: open stored API key: %w", err)
			}
			currentKey = opened
		case database.IsNotFound(readErr):
			currentKey = m.apiKey
		case readErr != nil:
			return fmt.Errorf("usercheck: read stored API key: %w", readErr)
		}

		if clearKey {
			key = ""
		} else if strings.TrimSpace(apiKey) != "" {
			key = strings.TrimSpace(apiKey)
		} else {
			key = currentKey
		}
		if strings.ContainsAny(key, "\r\n") {
			return fmt.Errorf("%w: API key contains a line break", ErrInvalidConfig)
		}
		if cfg.Enabled && key == "" {
			return ErrNotConfigured
		}
		var sealed any
		if key != "" {
			value, err := m.box.Seal(key)
			if err != nil {
				return fmt.Errorf("usercheck: seal API key: %w", err)
			}
			sealed = value
		}
		if _, err := tx.Exec(ctx, `INSERT INTO usercheck_config
			(id, enabled, exempt_domains, failure_mode, api_key, updated_at)
			VALUES (1, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled,
			exempt_domains = excluded.exempt_domains, failure_mode = excluded.failure_mode,
			api_key = excluded.api_key, updated_at = excluded.updated_at`,
			cfg.Enabled, string(domains), cfg.FailureMode, sealed, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("usercheck: save configuration: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	cfg.APIKeySet = key != ""
	if key != m.apiKey {
		m.backoffUntil = time.Time{}
	}
	m.cfg, m.apiKey = cfg, key
	return nil
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.FailureMode == "" {
		cfg.FailureMode = FailureReject
	}
	if cfg.FailureMode != FailureAllow && cfg.FailureMode != FailureReject {
		return Config{}, fmt.Errorf("%w: unknown failure mode", ErrInvalidConfig)
	}
	if len(cfg.ExemptDomains) > 100 {
		return Config{}, fmt.Errorf("%w: too many exempt domains", ErrInvalidConfig)
	}
	seen := make(map[string]bool, len(cfg.ExemptDomains))
	domains := make([]string, 0, len(cfg.ExemptDomains))
	for _, raw := range cfg.ExemptDomains {
		domain := strings.ToLower(strings.TrimSpace(raw))
		if len(domain) > 253 || !domainRE.MatchString(domain) {
			return Config{}, fmt.Errorf("%w: invalid exempt domain", ErrInvalidConfig)
		}
		if !seen[domain] {
			seen[domain] = true
			domains = append(domains, domain)
		}
	}
	cfg.ExemptDomains = domains
	return cfg, nil
}

// Check is called before an account or a changed address is written. The
// lookup cannot run under a database transaction: a provider round trip may
// take seconds, while the account's row lock must be short-lived.
func (m *Manager) Check(ctx context.Context, email string) error {
	m.mu.RLock()
	cfg := m.cfg
	key := m.apiKey
	backoffUntil := m.backoffUntil
	m.mu.RUnlock()
	if !cfg.Enabled || email == "" {
		return nil
	}
	domain := emailDomain(email)
	for _, exempt := range cfg.ExemptDomains {
		if domain == exempt {
			return nil
		}
	}
	var result Result
	var err error
	if time.Now().Before(backoffUntil) {
		err = ErrUnavailable
	} else {
		result, err = m.lookup(ctx, email, key)
	}
	if err != nil {
		if cfg.FailureMode == FailureAllow {
			slog.WarnContext(ctx, "UserCheck lookup failed; registration policy allows continuing", "error", err)
			return nil
		}
		return ErrUnavailable
	}
	if result.Disposable {
		return ErrDisposable
	}
	return nil
}

// Test always calls the API so an operator can check the key and inspect a
// domain even if the exemption list would skip it during registration.
func (m *Manager) Test(ctx context.Context, email string) (Result, error) {
	m.mu.RLock()
	key := m.apiKey
	backoffUntil := m.backoffUntil
	m.mu.RUnlock()
	if time.Now().Before(backoffUntil) {
		return Result{}, ErrUnavailable
	}
	return m.lookup(ctx, email, key)
}

func (m *Manager) lookup(ctx context.Context, email, key string) (Result, error) {
	if key == "" {
		return Result{}, ErrNotConfigured
	}
	if emailDomain(email) == "" {
		return Result{}, fmt.Errorf("%w: invalid email address", ErrInvalidConfig)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(lookupCtx, http.MethodGet,
		apiURL+url.PathEscape(email)+"?include_mx=false", nil)
	if err != nil {
		return Result{}, fmt.Errorf("%w: create request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		// net/http's error includes the URL, which carries the address. A
		// lookup failure belongs in logs without printing somebody's email.
		return Result{}, fmt.Errorf("%w: request failed", ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			m.backoff(resp.Header.Get("Retry-After"))
		}
		return Result{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024+1))
	if err != nil || len(body) > 16*1024 {
		return Result{}, fmt.Errorf("%w: invalid response size", ErrUnavailable)
	}
	var payload struct {
		Disposable *bool `json:"disposable"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Disposable == nil {
		return Result{}, fmt.Errorf("%w: invalid response", ErrUnavailable)
	}
	return Result{Disposable: *payload.Disposable}, nil
}

func (m *Manager) backoff(raw string) {
	// The data API uses 429 for both throughput and exhausted monthly
	// credits. Where it supplies no Retry-After, a short pause avoids a
	// rapid loop while leaving the operator's failure policy in control.
	wait := time.Minute
	if seconds, err := time.ParseDuration(strings.TrimSpace(raw) + "s"); err == nil && seconds > 0 {
		wait = seconds
	} else if at, err := http.ParseTime(raw); err == nil && time.Until(at) > 0 {
		wait = time.Until(at)
	}
	if wait > 5*time.Minute {
		wait = 5 * time.Minute
	}
	m.mu.Lock()
	until := time.Now().Add(wait)
	if until.After(m.backoffUntil) {
		m.backoffUntil = until
	}
	m.mu.Unlock()
}

func emailDomain(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 1 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// The test endpoint spends paid credits, so its cooldown is shared through
// the database instead of being reset by another process or a restart.
func (m *Manager) ClaimTest(ctx context.Context, now time.Time) (bool, error) {
	allowed := false
	err := m.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO usercheck_test_limiter (id, last_sent_at)
			VALUES (1, 0) ON CONFLICT (id) DO NOTHING`); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE usercheck_test_limiter SET last_sent_at = ?
			WHERE id = 1 AND last_sent_at <= ?`, now.UnixMilli(), now.Add(-TestCooldown).UnixMilli())
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		allowed = rows == 1
		return nil
	})
	return allowed, err
}
