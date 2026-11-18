package provider

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func (s *Store) sealHeaders(id string, headers map[string]string) (string, error) {
	if len(headers) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}
	return s.box.SealText("headers:"+id, string(data))
}

func (s *Store) OpenHeaders(id, raw string) (map[string]string, error) {
	if strings.HasPrefix(raw, "v1.") {
		if s.box == nil {
			return nil, secret.ErrDecrypt
		}
		plain, err := s.box.OpenText("headers:"+id, raw)
		if err != nil {
			return nil, err
		}
		raw = plain
	}
	return DecodeHeaders(id, raw), nil
}

// The management screen stays available after a key-lost restore so the
// operator can replace the headers. Calls to providers require decryption.
func (s *Store) listedHeaders(id, raw string) map[string]string {
	headers, err := s.OpenHeaders(id, raw)
	if err != nil {
		slog.Error("provider headers cannot be opened; re-enter the headers", "provider", id)
	}
	return headers
}

// SealLegacyHeaders migrates existing JSON objects in place before requests are served.
func (s *Store) SealLegacyHeaders(ctx context.Context) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, headers_json FROM providers WHERE headers_json NOT LIKE 'v1.%' AND headers_json <> '{}' AND headers_json <> ''`)
		if err != nil {
			return err
		}
		defer rows.Close()
		type entry struct{ id, raw string }
		var entries []entry
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.id, &e.raw); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for _, e := range entries {
			sealed, err := s.box.SealText("headers:"+e.id, e.raw)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE providers SET headers_json = ? WHERE id = ? AND headers_json = ?`, sealed, e.id, e.raw); err != nil {
				return err
			}
		}
		return nil
	})
}
