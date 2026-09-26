package mail

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

const TestSendCooldown = 5 * time.Minute

// Manager keeps the live sender in step with the one-row database override.
// The environment configuration remains effective only until an operator
// saves a database configuration.
type Manager struct {
	mu     sync.RWMutex
	db     *database.DB
	sender *Sender
	box    *secret.Box
	cfg    Config
}

// NewManager opens the mail override at startup. The constructor receives the
// same sender auth uses, so callers can wire it before constructing auth.
func NewManager(ctx context.Context, db *database.DB, sender *Sender, legacy Config, masterKey []byte) (*Manager, error) {
	box, err := secret.New(masterKey, "obsidian-arc/smtp-password")
	if err != nil {
		return nil, fmt.Errorf("mail: create password box: %w", err)
	}
	m := &Manager{db: db, sender: sender, box: box, cfg: normalizeConfig(legacy)}
	if err := m.read(ctx); err != nil {
		return nil, err
	}
	sender.Update(m.cfg)
	return m, nil
}

func (m *Manager) Config() (Config, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg, m.cfg.Password != ""
}

func (m *Manager) read(ctx context.Context) error {
	var cfg Config
	var sealed []byte
	err := m.db.QueryRow(ctx, `SELECT host, port, username, from_address, implicit_tls, public_url, password FROM mail_config WHERE id = 1`).Scan(
		&cfg.Host, &cfg.Port, &cfg.Username, &cfg.From, &cfg.ImplicitTLS, &cfg.PublicURL, &sealed,
	)
	if database.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mail: load configuration: %w", err)
	}
	if len(sealed) > 0 {
		cfg.Password, err = m.box.Open(sealed)
		if err != nil {
			return fmt.Errorf("mail: open stored password: %w", err)
		}
	}
	m.cfg = cfg
	return nil
}

// Save persists and activates a whole configuration. An empty password keeps
// the current secret unless clearPassword explicitly removes it.
func (m *Manager) Save(ctx context.Context, cfg Config, password string, clearPassword bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg = normalizeConfig(cfg)
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if err := m.db.Tx(ctx, func(tx *database.Tx) error {
		if err := settings.Lock(ctx, tx); err != nil {
			return err
		}

		// A manager can be behind another instance's credential rotation. Read
		// under the shared settings lock so a blank form field preserves the
		// database value that this save actually follows.
		var currentSealed []byte
		readErr := tx.QueryRow(ctx, `SELECT password FROM mail_config WHERE id = 1`).Scan(&currentSealed)
		currentPassword := ""
		switch {
		case readErr == nil && len(currentSealed) > 0:
			opened, err := m.box.Open(currentSealed)
			if err != nil {
				return fmt.Errorf("mail: open stored password: %w", err)
			}
			currentPassword = opened
		case database.IsNotFound(readErr):
			// Keep the legacy environment credential when creating the first
			// database override without entering a replacement password.
			currentPassword = m.cfg.Password
		case readErr != nil:
			return fmt.Errorf("mail: read stored password: %w", readErr)
		}

		if clearPassword {
			cfg.Password = ""
		} else if password != "" {
			cfg.Password = password
		} else {
			cfg.Password = currentPassword
		}
		var sealed any
		if cfg.Password != "" {
			value, err := m.box.Seal(cfg.Password)
			if err != nil {
				return fmt.Errorf("mail: seal password: %w", err)
			}
			sealed = value
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_config
			(id, host, port, username, from_address, implicit_tls, public_url, password, updated_at)
			VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET host = excluded.host, port = excluded.port,
			username = excluded.username, from_address = excluded.from_address,
			implicit_tls = excluded.implicit_tls, public_url = excluded.public_url,
			password = excluded.password, updated_at = excluded.updated_at`,
			cfg.Host, cfg.Port, cfg.Username, cfg.From, cfg.ImplicitTLS, cfg.PublicURL, sealed, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("mail: save configuration: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	m.cfg = cfg
	m.sender.Update(cfg)
	return nil
}

func (m *Manager) Sender() *Sender { return m.sender }

func normalizeConfig(cfg Config) Config {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.PublicURL = strings.TrimSpace(cfg.PublicURL)
	return cfg
}

// ClaimTestSend records the shared, instance-wide test-mail cooldown before a
// send starts. Keeping the read-and-write in one transaction makes concurrent
// administrators and replicas contend on the database row rather than one
// process-local mutex.
func (m *Manager) ClaimTestSend(ctx context.Context, now time.Time) (bool, error) {
	allowed := false
	cutoff := now.Add(-TestSendCooldown).UnixMilli()
	err := m.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_test_limiter (id, last_sent_at) VALUES (1, 0) ON CONFLICT (id) DO NOTHING`); err != nil {
			return fmt.Errorf("mail: initialize test limiter: %w", err)
		}
		result, err := tx.Exec(ctx, `UPDATE mail_test_limiter SET last_sent_at = ? WHERE id = 1 AND last_sent_at <= ?`, now.UnixMilli(), cutoff)
		if err != nil {
			return fmt.Errorf("mail: claim test send: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("mail: inspect test limiter: %w", err)
		}
		allowed = updated == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return allowed, nil
}
