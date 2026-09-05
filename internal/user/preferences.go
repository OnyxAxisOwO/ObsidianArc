package user

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Preferences is the interface state that should follow an account between
// devices: theme, accent, wallpaper, which model to open a new chat with.
//
// It is one JSON document rather than a column per setting, because it is
// pure presentation and grows every time the interface does. Adding a toggle
// should not mean a migration, and none of it is ever queried or joined on.
// The `users` row, which is read on every authenticated request, stays small.
//
// The server does not interpret most of these. It stores what the client
// sends, bounded in size and shape, and hands it back.
type Preferences struct {
	Theme            string          `json:"theme,omitempty"`
	Accent           string          `json:"accent,omitempty"`
	CustomAccent     string          `json:"custom_accent,omitempty"`
	Wallpaper        json.RawMessage `json:"wallpaper,omitempty"`
	DefaultModelID   string          `json:"default_model_id,omitempty"`
	ReasoningEnabled *bool           `json:"reasoning_enabled,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	Language         string          `json:"language,omitempty"`
	SendOnEnter      *bool           `json:"send_on_enter,omitempty"`
	RailCollapsed    *bool           `json:"rail_collapsed,omitempty"`
}

// MaxPreferencesBytes bounds the document. Generous enough for an inline
// wallpaper thumbnail, small enough that it cannot be used as free storage.
const MaxPreferencesBytes = 512 * 1024

type PreferenceStore struct{ db *database.DB }

func NewPreferenceStore(db *database.DB) *PreferenceStore { return &PreferenceStore{db: db} }

// Get returns the stored document. A user who has never saved one gets an
// empty object rather than an error, so the caller has no absent case.
func (p *PreferenceStore) Get(ctx context.Context, userID string) (json.RawMessage, error) {
	var raw string
	err := p.db.QueryRow(ctx, `SELECT data FROM user_preferences WHERE user_id = ?`, userID).Scan(&raw)
	if err != nil {
		if database.IsNotFound(err) {
			return json.RawMessage(`{}`), nil
		}
		return nil, fmt.Errorf("user: load preferences: %w", err)
	}
	if raw == "" {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(raw), nil
}

// Merge applies a partial update, so a client that changes the theme does not
// have to send back every other preference it did not touch — and cannot
// clobber one written by another device in between.
func (p *PreferenceStore) Merge(ctx context.Context, userID string, patch map[string]json.RawMessage) (json.RawMessage, error) {
	var merged json.RawMessage

	err := p.db.Tx(ctx, func(tx *database.Tx) error {
		var raw string
		err := tx.QueryRow(ctx, `SELECT data FROM user_preferences WHERE user_id = ?`, userID).Scan(&raw)
		if err != nil && !database.IsNotFound(err) {
			return fmt.Errorf("user: load preferences: %w", err)
		}

		current := map[string]json.RawMessage{}
		if raw != "" {
			// A corrupted document is replaced rather than propagated: the
			// alternative is a user who can never save a preference again.
			_ = json.Unmarshal([]byte(raw), &current)
		}
		for key, value := range patch {
			if len(value) == 0 || string(value) == "null" {
				delete(current, key)
				continue
			}
			current[key] = value
		}

		encoded, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("user: encode preferences: %w", err)
		}
		if len(encoded) > MaxPreferencesBytes {
			return fmt.Errorf("user: preferences exceed %d bytes", MaxPreferencesBytes)
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO user_preferences (user_id, data, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (user_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
			userID, string(encoded), time.Now().UnixMilli())
		if err != nil {
			return fmt.Errorf("user: save preferences: %w", err)
		}
		merged = encoded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return merged, nil
}
