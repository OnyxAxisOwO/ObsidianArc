package settings

import (
	"context"
	"log/slog"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func (s *Service) isSecret(key string, installing []Definition) bool {
	switch key {
	case TurnstileSecretKey, OAuthGitHubSecret, OAuthGoogleSecret, OAuthOIDCClientSecret:
		return true
	}
	for _, d := range installing {
		if d.Key == key {
			return d.Secret
		}
	}
	d, _ := s.definition(key)
	return d.Secret
}

func (s *Service) openValue(key, plain, sealed string) string {
	if sealed == "" {
		return plain
	}
	if s.box != nil {
		if v, err := s.box.OpenText(key, sealed); err == nil {
			return v
		}
	}
	// An operator restoring without the original key can still sign in and
	// replace a credential. The unreadable row remains intact.
	slog.Error("setting credential cannot be opened; re-enter it with the instance key", "setting", key)
	return ""
}

// SealLegacyCredentials runs after installed packages declare which of their settings are secrets.
func (s *Service) SealLegacyCredentials(ctx context.Context) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		rows, err := tx.Query(ctx, `SELECT key,value FROM settings WHERE value <> '' AND sealed_value = ''`)
		if err != nil {
			return err
		}
		defer rows.Close()
		values := map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			if s.isSecret(k, nil) {
				values[k] = v
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for key, value := range values {
			sealed, err := s.box.SealText(key, value)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE settings SET value = '', sealed_value = ? WHERE key = ? AND value = ? AND sealed_value = ''`, sealed, key, value); err != nil {
				return err
			}
		}
		return nil
	})
}
