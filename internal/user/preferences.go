package user

import (
	"context"
	"encoding/json"
	"errors"
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

// --- wallpaper ---------------------------------------------------------------

// MaxWallpaperBytes bounds the stored image. The client downscales to roughly
// a screen's width first; the cap is the backstop.
const MaxWallpaperBytes = 4 * 1024 * 1024

var allowedWallpaperMedia = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/avif": true,
}

var (
	ErrWallpaperUnsupported = errors.New("user: wallpaper must be JPEG, PNG, WebP or AVIF")
	ErrWallpaperTooLarge    = errors.New("user: wallpaper is too large")
	ErrNoWallpaper          = errors.New("user: no wallpaper set")
)

// SetWallpaper replaces the stored image and returns the version stamp, which
// the client puts in the URL so a replacement is visible without a reload.
func (p *PreferenceStore) SetWallpaper(ctx context.Context, userID, mime string, data []byte) (int64, error) {
	if !allowedWallpaperMedia[mime] {
		return 0, ErrWallpaperUnsupported
	}
	if len(data) == 0 || len(data) > MaxWallpaperBytes {
		return 0, ErrWallpaperTooLarge
	}

	at := time.Now().UnixMilli()
	_, err := p.db.Exec(ctx,
		`INSERT INTO user_preferences (user_id, data, updated_at, wallpaper_mime, wallpaper_data, wallpaper_at)
		 VALUES (?, '{}', ?, ?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET
		   wallpaper_mime = excluded.wallpaper_mime,
		   wallpaper_data = excluded.wallpaper_data,
		   wallpaper_at = excluded.wallpaper_at,
		   updated_at = excluded.updated_at`,
		userID, at, mime, data, at)
	if err != nil {
		return 0, fmt.Errorf("user: save wallpaper: %w", err)
	}
	return at, nil
}

// Wallpaper returns the stored image for its owner. The ownership test is the
// query, not a check on the result.
func (p *PreferenceStore) Wallpaper(ctx context.Context, userID string) (mime string, data []byte, at int64, err error) {
	err = p.db.QueryRow(ctx,
		`SELECT wallpaper_mime, wallpaper_data, wallpaper_at FROM user_preferences WHERE user_id = ?`,
		userID).Scan(&mime, &data, &at)
	if err != nil {
		if database.IsNotFound(err) {
			return "", nil, 0, ErrNoWallpaper
		}
		return "", nil, 0, fmt.Errorf("user: read wallpaper: %w", err)
	}
	if mime == "" || len(data) == 0 {
		return "", nil, 0, ErrNoWallpaper
	}
	return mime, data, at, nil
}

func (p *PreferenceStore) ClearWallpaper(ctx context.Context, userID string) error {
	_, err := p.db.Exec(ctx,
		`UPDATE user_preferences SET wallpaper_mime = '', wallpaper_data = NULL, wallpaper_at = 0 WHERE user_id = ?`,
		userID)
	if err != nil {
		return fmt.Errorf("user: clear wallpaper: %w", err)
	}
	return nil
}
