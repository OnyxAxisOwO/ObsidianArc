// Package settings is the instance-wide key/value store an administrator can
// change without a restart: whether registration is open, what the site is
// called, which group new accounts join.
//
// The whole table is a handful of rows read on nearly every request, so it is
// cached in memory and refreshed on write. One process owns the database, so
// the cache cannot go stale behind its back.
package settings

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Known keys. Anything not listed here is still storable — the admin UI only
// offers these, and a future module can add its own without a migration.
const (
	SiteName             = "site.name"
	SiteDescription      = "site.description"
	RegistrationEnabled  = "registration.enabled"
	RegistrationGroup    = "registration.default_group"
	AdminsBypassQuota    = "quota.admins_bypass"
	DefaultSystemPrompt  = "chat.default_system_prompt"
	ConversationMaxTurns = "chat.max_turns"
)

// Defaults are what a fresh instance behaves like, and what a deleted row
// falls back to. Nothing reads a setting without one.
var Defaults = map[string]string{
	SiteName:             "Obsidian Arc",
	SiteDescription:      "",
	RegistrationEnabled:  "true",
	RegistrationGroup:    "",
	AdminsBypassQuota:    "true",
	DefaultSystemPrompt:  "",
	ConversationMaxTurns: "40",
}

type Service struct {
	db *database.DB

	mu     sync.RWMutex
	values map[string]string
}

func New(db *database.DB) *Service {
	return &Service{db: db, values: map[string]string{}}
}

// Load reads the table into memory. Called once at boot; after that the cache
// is kept current by Set.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return fmt.Errorf("settings: load: %w", err)
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("settings: scan: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("settings: load: %w", err)
	}

	s.mu.Lock()
	s.values = values
	s.mu.Unlock()
	return nil
}

func (s *Service) Get(key string) string {
	s.mu.RLock()
	value, ok := s.values[key]
	s.mu.RUnlock()
	if ok {
		return value
	}
	return Defaults[key]
}

func (s *Service) Bool(key string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(s.Get(key)))
	if err != nil {
		return false
	}
	return value
}

func (s *Service) Int(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(s.Get(key)))
	if err != nil {
		return fallback
	}
	return value
}

// All returns every known key with its effective value, so the admin screen
// can render settings that have never been written.
func (s *Service) All() map[string]string {
	out := make(map[string]string, len(Defaults))
	for key, value := range Defaults {
		out[key] = value
	}
	s.mu.RLock()
	for key, value := range s.values {
		out[key] = value
	}
	s.mu.RUnlock()
	return out
}

func (s *Service) Set(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("settings: set %s: %w", key, err)
	}
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

func (s *Service) SetMany(ctx context.Context, values map[string]string) error {
	now := time.Now().UnixMilli()
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		for key, value := range values {
			if _, err := tx.Exec(ctx,
				`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
				key, value, now); err != nil {
				return fmt.Errorf("settings: set %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	for key, value := range values {
		s.values[key] = value
	}
	s.mu.Unlock()
	return nil
}
