package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeManifest(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("manifest response was not valid JSON: %v\nbody: %s", err, rec.Body.String())
	}
	return doc
}

func TestManifestHandlerServesResolvedFields(t *testing.T) {
	handler := ManifestHandler(func() Manifest {
		return Manifest{
			Name:            "My Instance",
			ShortName:       "MI",
			Description:     "A private chat server.",
			ThemeColor:      "#112233",
			BackgroundColor: "#445566",
			IconURL:         "/icon.png",
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache — an operator's save must reach the next install prompt", cc)
	}

	doc := decodeManifest(t, rec)
	if doc["name"] != "My Instance" || doc["short_name"] != "MI" {
		t.Errorf("name/short_name not passed through: %+v", doc)
	}
	if doc["description"] != "A private chat server." {
		t.Errorf("description not passed through: %+v", doc)
	}
	if doc["theme_color"] != "#112233" || doc["background_color"] != "#445566" {
		t.Errorf("colours not passed through: %+v", doc)
	}
	if doc["start_url"] != "/" || doc["display"] != "standalone" {
		t.Errorf("unexpected start_url/display: %+v", doc)
	}

	icons, ok := doc["icons"].([]any)
	if !ok || len(icons) != 1 {
		t.Fatalf("expected exactly one icon, got %+v", doc["icons"])
	}
	entry := icons[0].(map[string]any)
	if entry["src"] != "/icon.png" || entry["type"] != "image/png" {
		t.Errorf("custom icon entry wrong: %+v", entry)
	}
}

// Everything an operator never configured: the built-in icon, no name of its
// own, and no colour fields at all — a manifest with a "theme_color": ""
// would tell a browser to theme the chrome white rather than to leave it
// alone, which is a different thing from never having said.
func TestManifestHandlerFallsBackToBuiltins(t *testing.T) {
	handler := ManifestHandler(func() Manifest { return Manifest{Name: "Obsidian Arc"} })

	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	doc := decodeManifest(t, rec)
	if doc["short_name"] != "Obsidian Arc" {
		t.Errorf("short_name should fall back to name, got %+v", doc["short_name"])
	}
	if _, present := doc["description"]; present {
		t.Errorf("empty description should be omitted, got %+v", doc["description"])
	}
	if _, present := doc["theme_color"]; present {
		t.Errorf("empty theme_color should be omitted, got %+v", doc["theme_color"])
	}
	if _, present := doc["background_color"]; present {
		t.Errorf("empty background_color should be omitted, got %+v", doc["background_color"])
	}

	icons := doc["icons"].([]any)
	entry := icons[0].(map[string]any)
	if entry["type"] != "image/svg+xml" || !strings.HasPrefix(entry["src"].(string), "data:image/svg+xml,") {
		t.Errorf("expected the built-in inline SVG icon, got %+v", entry)
	}
}

// A blank name reaching the handler at all means even the site.name fallback
// the caller in server.go applies was empty — the one case this package
// cannot leave to the caller, because a manifest cannot ship with none.
func TestManifestHandlerNeverShipsAnEmptyName(t *testing.T) {
	handler := ManifestHandler(func() Manifest { return Manifest{} })

	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	doc := decodeManifest(t, rec)
	if doc["name"] == "" || doc["short_name"] == "" {
		t.Errorf("manifest shipped with an empty name: %+v", doc)
	}
}

func TestManifestHandlerGuessesIconMediaType(t *testing.T) {
	cases := map[string]string{
		"/icon.svg":                        "image/svg+xml",
		"/icon.webp":                       "image/webp",
		"/icon.jpg":                        "image/jpeg",
		"/icon.gif":                        "image/gif",
		"/icon.png":                        "image/png",
		"/favicon.ico":                     "image/x-icon",
		"/icon.svg?v=3":                    "image/svg+xml",
		"/Logo.SVG?x=1":                    "image/svg+xml",
		"/brand/logo.webp#frag":            "image/webp",
		"data:image/png;base64,AAAA":       "image/png",
		"data:image/svg+xml;base64,AAAA==": "image/svg+xml",
	}
	for url, want := range cases {
		if got := iconMediaType(url); got != want {
			t.Errorf("iconMediaType(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestManifestHandlerRejectsWriteMethods(t *testing.T) {
	handler := ManifestHandler(func() Manifest { return Manifest{Name: "Obsidian Arc"} })

	req := httptest.NewRequest(http.MethodPost, "/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST got status %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
