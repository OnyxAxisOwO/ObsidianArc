package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// An AVIF icon was declared as image/png, the default for any extension the
// guess did not list.
func TestAnAVIFIconIsDeclaredAsAVIF(t *testing.T) {
	if got := iconMediaType("/brand/logo.avif?v=2"); got != "image/avif" {
		t.Errorf("iconMediaType = %q, want image/avif", got)
	}
}

// The site logo's URL has no extension, so every type but PNG was declared as
// PNG. The stored type is passed in and must be the one declared.
func TestTheLogoFallbackDeclaresTheStoredType(t *testing.T) {
	handler := ManifestHandler(func() Manifest {
		return Manifest{Name: "Obsidian Arc", IconURL: "/api/site/logo?v=7", IconType: "image/avif"}
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))

	icons := decodeManifest(t, rec)["icons"].([]any)
	entry := icons[0].(map[string]any)
	if entry["src"] != "/api/site/logo?v=7" || entry["type"] != "image/avif" {
		t.Errorf("icon = %+v, want the logo URL declared as image/avif", entry)
	}
}

// Without a stored type the URL is all there is to go on, which is the old
// behaviour for an explicit icon URL.
func TestAnExplicitIconURLStillGuessesItsType(t *testing.T) {
	handler := ManifestHandler(func() Manifest {
		return Manifest{Name: "Obsidian Arc", IconURL: "/icons/app.webp"}
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))

	icons := decodeManifest(t, rec)["icons"].([]any)
	if entry := icons[0].(map[string]any); entry["type"] != "image/webp" {
		t.Errorf("icon type = %v, want image/webp", entry["type"])
	}
}

// defaultIcon is a copy of the favicon in web/index.html, kept by hand because
// the manifest must not read the shell at request time. This test is the only
// thing that notices when the two drift apart.
func TestTheDefaultIconMatchesTheOneInIndexHTML(t *testing.T) {
	shell, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatalf("read the shell: %v", err)
	}
	if !strings.Contains(string(shell), `href="`+defaultIcon+`"`) {
		t.Errorf("web/index.html does not carry the favicon defaultIcon describes; the two must change together")
	}
}

// The manifest refuses writes in the same envelope as every other handler.
func TestTheManifestRefusalUsesTheStandardEnvelope(t *testing.T) {
	handler := ManifestHandler(func() Manifest { return Manifest{Name: "Obsidian Arc"} })

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/manifest.webmanifest", nil))

	if !strings.Contains(rec.Body.String(), `"method_not_allowed"`) {
		t.Errorf("refusal body = %q, want the standard error envelope", rec.Body.String())
	}
}
