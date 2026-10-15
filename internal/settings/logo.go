package settings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

const MaxLogoBytes = 4 * 1024 * 1024 // 4 MB

var (
	ErrLogoUnsupported = errors.New("settings: logo must be PNG, JPEG, SVG, WebP, AVIF, GIF or ICO")
	ErrLogoTooLarge    = errors.New("settings: logo is too large")
	ErrNoLogo          = errors.New("settings: no site logo set")
)

// DetectLogoMedia verifies the file's magic signature bytes or markup.
// Logos can be raster (PNG, JPEG, WebP, AVIF, GIF, ICO) or vector (SVG).
func DetectLogoMedia(data []byte) (string, error) {
	if len(data) < 4 {
		return "", ErrLogoUnsupported
	}
	// PNG: \x89PNG\r\n\x1a\n
	if len(data) >= 8 && bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png", nil
	}
	// JPEG: \xFF\xD8\xFF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg", nil
	}
	// GIF: GIF87a or GIF89a
	if len(data) >= 6 && (bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))) {
		return "image/gif", nil
	}
	// WebP: RIFF....WEBP
	if len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "image/webp", nil
	}
	// ICO: \x00\x00\x01\x00
	if bytes.HasPrefix(data, []byte("\x00\x00\x01\x00")) {
		return "image/x-icon", nil
	}
	// AVIF: ....ftypavif or ....ftypavis
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		brand := string(data[8:12])
		if brand == "avif" || brand == "avis" {
			return "image/avif", nil
		}
		limit := min(len(data), 64)
		if bytes.Contains(data[8:limit], []byte("avif")) || bytes.Contains(data[8:limit], []byte("avis")) {
			return "image/avif", nil
		}
	}
	// SVG: look for <svg in the first 1024 bytes (stripping UTF-8 BOM if present)
	trimmed := bytes.TrimSpace(data)
	trimmed = bytes.TrimPrefix(trimmed, []byte("\xef\xbb\xbf"))
	if len(trimmed) > 4 && trimmed[0] == '<' {
		checkLimit := min(len(trimmed), 1024)
		snippet := bytes.ToLower(trimmed[:checkLimit])
		if bytes.Contains(snippet, []byte("<svg")) && !bytes.Contains(snippet, []byte("<html")) && !bytes.Contains(snippet, []byte("<script")) {
			return "image/svg+xml", nil
		}
	}

	return "", ErrLogoUnsupported
}

// SiteLogoUpdatedAt returns the timestamp of the current custom site logo, or 0 if none is set.
func (s *Service) SiteLogoUpdatedAt() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.siteLogoAt
}

// SetSiteLogo stores or updates the custom site logo image.
func (s *Service) SetSiteLogo(ctx context.Context, mime string, data []byte) (int64, error) {
	if len(data) == 0 || len(data) > MaxLogoBytes {
		return 0, ErrLogoTooLarge
	}

	detectedMime, err := DetectLogoMedia(data)
	if err != nil {
		return 0, ErrLogoUnsupported
	}
	mime = detectedMime

	at := time.Now().UnixMilli()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.db.Exec(ctx,
		`INSERT INTO site_logo (id, mime, data, updated_at)
		 VALUES ('default', ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   mime = excluded.mime,
		   data = excluded.data,
		   updated_at = excluded.updated_at`,
		mime, data, at)
	if err != nil {
		return 0, fmt.Errorf("settings: save site logo: %w", err)
	}

	s.mu.Lock()
	s.siteLogoAt = at
	s.mu.Unlock()
	return at, nil
}

// GetSiteLogo retrieves the mime, binary data, and update timestamp of the custom site logo.
func (s *Service) GetSiteLogo(ctx context.Context) (mime string, data []byte, at int64, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT mime, data, updated_at FROM site_logo WHERE id = 'default'`).Scan(&mime, &data, &at)
	if err != nil {
		if database.IsNotFound(err) {
			return "", nil, 0, ErrNoLogo
		}
		return "", nil, 0, fmt.Errorf("settings: read site logo: %w", err)
	}
	return mime, data, at, nil
}

// DeleteSiteLogo removes the custom site logo.
func (s *Service) DeleteSiteLogo(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(ctx, `DELETE FROM site_logo WHERE id = 'default'`)
	if err != nil {
		return fmt.Errorf("settings: delete site logo: %w", err)
	}

	s.mu.Lock()
	s.siteLogoAt = 0
	s.mu.Unlock()
	return nil
}
