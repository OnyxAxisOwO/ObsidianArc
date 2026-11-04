package announcement

import (
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// The announcement refusals answer with codes the admin and reader screens
// translate, and the title refusal carries the limit it quotes.
func TestAnnouncementRefusalsCarryCodesAndTheirLimits(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"not found", ErrNotFound, "announcement_not_found"},
		{"title", ErrInvalidTitle, "announcement_title_invalid"},
		{"body", ErrInvalidBody, "announcement_too_long"},
		{"mode", ErrInvalidMode, "announcement_mode_invalid"},
	}
	for _, c := range cases {
		var apiErr *httpx.Error
		if !errors.As(TranslateError(c.err), &apiErr) {
			t.Fatalf("%s: TranslateError did not return an httpx error", c.name)
		}
		if apiErr.Code != c.code {
			t.Errorf("%s: code = %q, want %q", c.name, apiErr.Code, c.code)
		}
	}

	var title *httpx.Error
	errors.As(TranslateError(ErrInvalidTitle), &title)
	if title.Details["max"] != MaxTitleChars {
		t.Errorf("title details = %v, want the title limit %d", title.Details, MaxTitleChars)
	}
}

// The display modes are a set lookup, and exactly the named ones are valid.
func TestDisplayModeValidityIsTheNamedSet(t *testing.T) {
	for _, m := range Modes {
		if !m.Valid() {
			t.Errorf("mode %q is not valid", m)
		}
	}
	if DisplayMode("banner").Valid() {
		t.Error("a mode outside the set is valid")
	}
}
