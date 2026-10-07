package settings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// The signed-out screens' variants keep the bare names they shipped with, so a
// database that stored them before the signed-in set existed still finds
// them. The signed-in set is the same six behind an "app_" prefix: one
// table and one endpoint, because the only thing that differs is which
// screens draw them.
const (
	LoginBgLandscapeLight = "landscape_light"
	LoginBgLandscapeDark  = "landscape_dark"
	LoginBgPortraitLight  = "portrait_light"
	LoginBgPortraitDark   = "portrait_dark"
	// A touch screen at least 600px on both sides, whichever way it is held:
	// neither the desktop shape nor the phone's.
	LoginBgTabletLight = "tablet_light"
	LoginBgTabletDark  = "tablet_dark"

	AppBgPrefix = "app_"
)

var ValidLoginBackgroundVariants = map[string]bool{
	LoginBgLandscapeLight:               true,
	LoginBgLandscapeDark:                true,
	LoginBgPortraitLight:                true,
	LoginBgPortraitDark:                 true,
	LoginBgTabletLight:                  true,
	LoginBgTabletDark:                   true,
	AppBgPrefix + LoginBgLandscapeLight: true,
	AppBgPrefix + LoginBgLandscapeDark:  true,
	AppBgPrefix + LoginBgPortraitLight:  true,
	AppBgPrefix + LoginBgPortraitDark:   true,
	AppBgPrefix + LoginBgTabletLight:    true,
	AppBgPrefix + LoginBgTabletDark:     true,
}

// HTMLBackgroundMime marks a background that is a page rather than a
// picture. It is stored in the same column an image's type is, so the row
// says which it is and nothing beside it has to agree.
const HTMLBackgroundMime = "text/html; charset=utf-8"

// MaxHTMLBackgroundBytes is generous for a page of CSS and a script, and
// small enough that nobody mistakes the field for a place to inline a video.
const MaxHTMLBackgroundBytes = 512 * 1024

// Background is what the public site description needs about one variant:
// when it last changed, for the cache-busting query, and whether it is drawn
// in a frame or as an image.
type Background struct {
	UpdatedAt int64
	HTML      bool
}

func NormalizeVariant(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "-", "_")
}

const MaxLoginBackgroundBytes = 6 * 1024 * 1024

var (
	ErrLoginBackgroundUnsupported = errors.New("settings: login background must be JPEG, PNG, WebP or AVIF")
	ErrLoginBackgroundTooLarge    = errors.New("settings: login background is too large")
	ErrNoLoginBackground          = errors.New("settings: no login background set")
)

// DetectImageMedia verifies the file's magic signature bytes directly.
// Relying on client-supplied Content-Type headers alone is vulnerable to MIME
// spoofing, and standard library http.DetectContentType does not recognize AVIF.
func DetectImageMedia(data []byte) (string, error) {
	if len(data) < 12 {
		return "", ErrLoginBackgroundUnsupported
	}
	// PNG: \x89PNG\r\n\x1a\n
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png", nil
	}
	// JPEG: \xFF\xD8\xFF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg", nil
	}
	// WebP: RIFF....WEBP
	if bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "image/webp", nil
	}
	// AVIF: ....ftypavif or ....ftypavis or compatible brands in the ftyp box
	if bytes.Equal(data[4:8], []byte("ftyp")) {
		brand := string(data[8:12])
		if brand == "avif" || brand == "avis" {
			return "image/avif", nil
		}
		limit := min(len(data), 64)
		if bytes.Contains(data[8:limit], []byte("avif")) || bytes.Contains(data[8:limit], []byte("avis")) {
			return "image/avif", nil
		}
	}
	return "", ErrLoginBackgroundUnsupported
}

// LoginBackgrounds returns every stored variant, both sets, keyed by name.
func (s *Service) LoginBackgrounds() map[string]Background {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Background, len(s.loginBackgrounds))
	for k, v := range s.loginBackgrounds {
		out[k] = v
	}
	return out
}

// SetHTMLBackground stores a page of markup as a variant. It is not checked
// or cleaned: it is served under a sandbox that gives it no origin (see
// auth's getLoginBackground), so what it can do is draw, which is the point.
func (s *Service) SetHTMLBackground(ctx context.Context, variant, html string) (int64, error) {
	if len(html) > MaxHTMLBackgroundBytes {
		return 0, ErrLoginBackgroundTooLarge
	}
	if strings.TrimSpace(html) == "" {
		return 0, ErrLoginBackgroundUnsupported
	}
	return s.storeBackground(ctx, variant, HTMLBackgroundMime, []byte(html))
}

// SetLoginBackground stores or updates a login background image for a given variant.
func (s *Service) SetLoginBackground(ctx context.Context, variant, mime string, data []byte) (int64, error) {
	variant = NormalizeVariant(variant)
	if !ValidLoginBackgroundVariants[variant] {
		return 0, fmt.Errorf("settings: unknown variant %q", variant)
	}
	if len(data) == 0 || len(data) > MaxLoginBackgroundBytes {
		return 0, ErrLoginBackgroundTooLarge
	}

	detectedMime, err := DetectImageMedia(data)
	if err != nil {
		return 0, ErrLoginBackgroundUnsupported
	}
	return s.storeBackground(ctx, variant, detectedMime, data)
}

func (s *Service) storeBackground(ctx context.Context, variant, mime string, data []byte) (int64, error) {
	variant = NormalizeVariant(variant)
	if !ValidLoginBackgroundVariants[variant] {
		return 0, fmt.Errorf("settings: unknown variant %q", variant)
	}
	at := time.Now().UnixMilli()
	_, err := s.db.Exec(ctx,
		`INSERT INTO login_backgrounds (variant, mime, data, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (variant) DO UPDATE SET
		   mime = excluded.mime,
		   data = excluded.data,
		   updated_at = excluded.updated_at`,
		variant, mime, data, at)
	if err != nil {
		return 0, fmt.Errorf("settings: save login background: %w", err)
	}

	s.mu.Lock()
	if s.loginBackgrounds == nil {
		s.loginBackgrounds = map[string]Background{}
	}
	s.loginBackgrounds[variant] = Background{UpdatedAt: at, HTML: mime == HTMLBackgroundMime}
	s.mu.Unlock()
	return at, nil
}

// GetLoginBackground retrieves the mime, binary data, and update timestamp for a given variant.
func (s *Service) GetLoginBackground(ctx context.Context, variant string) (mime string, data []byte, at int64, err error) {
	variant = NormalizeVariant(variant)
	if !ValidLoginBackgroundVariants[variant] {
		return "", nil, 0, ErrNoLoginBackground
	}

	err = s.db.QueryRow(ctx,
		`SELECT mime, data, updated_at FROM login_backgrounds WHERE variant = ?`,
		variant).Scan(&mime, &data, &at)
	if err != nil {
		if database.IsNotFound(err) {
			return "", nil, 0, ErrNoLoginBackground
		}
		return "", nil, 0, fmt.Errorf("settings: read login background: %w", err)
	}
	return mime, data, at, nil
}

// DeleteLoginBackground removes a background image variant.
func (s *Service) DeleteLoginBackground(ctx context.Context, variant string) error {
	variant = NormalizeVariant(variant)
	if !ValidLoginBackgroundVariants[variant] {
		return nil
	}

	_, err := s.db.Exec(ctx, `DELETE FROM login_backgrounds WHERE variant = ?`, variant)
	if err != nil {
		return fmt.Errorf("settings: delete login background: %w", err)
	}

	s.mu.Lock()
	delete(s.loginBackgrounds, variant)
	s.mu.Unlock()
	return nil
}
