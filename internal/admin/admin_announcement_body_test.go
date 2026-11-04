package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/announcement"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// A body at the character ceiling has to get past the request cap however the
// client escapes it. The longest form is a character outside the BMP written as
// its surrogate pair: twelve bytes for one character, and one character the
// store counts. A cap sized for six-byte escapes refused that body with a 413.
func TestAnnouncementBodyAtTheCharacterCeilingFitsTheRequestCap(t *testing.T) {
	forms := map[string]string{
		"plain ASCII":            "a",
		"CJK":                    "界",
		"escaped angle bracket":  `<`,
		"escaped surrogate pair": `😀`,
	}
	for name, unit := range forms {
		t.Run(name, func(t *testing.T) {
			payload := `{"title":"t","body":"` + strings.Repeat(unit, announcement.MaxBodyChars) + `"}`
			var body announcementRequest
			req := httptest.NewRequest(http.MethodPost, "/api/admin/announcements", strings.NewReader(payload))
			if err := httpx.DecodeJSON(httptest.NewRecorder(), req, &body, maxAnnouncementBody); err != nil {
				t.Fatalf("a body of %d characters was refused: %v", announcement.MaxBodyChars, err)
			}
			if n := utf8.RuneCountInString(body.Body); n != announcement.MaxBodyChars {
				t.Fatalf("decoded body has %d characters, want %d", n, announcement.MaxBodyChars)
			}
		})
	}
}
