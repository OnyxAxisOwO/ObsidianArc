package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The manifest is what a browser reads to draw the "install" prompt. Public,
// like the shell itself, and reachable before anyone has signed in.
func TestManifestServesConfiguredValues(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		settings.PWAName:            "ACME App",
		settings.PWAShortName:       "ACME",
		settings.PWADescription:     "The internal chat assistant.",
		settings.PWAThemeColor:      "#112233",
		settings.PWABackgroundColor: "#445566",
		settings.PWAIconURL:         "/icon.png",
	}, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("save PWA settings: %d %s", response.Code, response.Body.String())
	}

	manifest := in.do(http.MethodGet, "/manifest.webmanifest", nil, nil)
	if manifest.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest: %d %s", manifest.Code, manifest.Body.String())
	}
	if ct := manifest.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}

	var doc map[string]any
	if err := json.Unmarshal(manifest.Body.Bytes(), &doc); err != nil {
		t.Fatalf("manifest was not valid JSON: %v", err)
	}
	if doc["name"] != "ACME App" || doc["short_name"] != "ACME" {
		t.Errorf("name/short_name = %v / %v", doc["name"], doc["short_name"])
	}
	if doc["description"] != "The internal chat assistant." {
		t.Errorf("description = %v", doc["description"])
	}
	if doc["theme_color"] != "#112233" || doc["background_color"] != "#445566" {
		t.Errorf("colours = %v / %v", doc["theme_color"], doc["background_color"])
	}
	icons, _ := doc["icons"].([]any)
	if len(icons) != 1 || icons[0].(map[string]any)["src"] != "/icon.png" {
		t.Errorf("icons = %v", doc["icons"])
	}
}

// Nothing configured is the common case, and it must still produce a usable
// manifest: the site's own name and description, the interface's own default
// colours, and the built-in icon rather than a broken reference.
func TestManifestFallsBackToSiteIdentity(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		settings.SiteName:        "ACME Chat",
		settings.SiteDescription: "Ask #it-help before filing a ticket.",
	}, admin); response.Code != http.StatusOK {
		t.Fatalf("save site identity: %d %s", response.Code, response.Body.String())
	}

	manifest := in.do(http.MethodGet, "/manifest.webmanifest", nil, nil)
	if manifest.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest: %d %s", manifest.Code, manifest.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(manifest.Body.Bytes(), &doc); err != nil {
		t.Fatalf("manifest was not valid JSON: %v", err)
	}
	if doc["name"] != "ACME Chat" || doc["short_name"] != "ACME Chat" {
		t.Errorf("name/short_name = %v / %v, want both to fall back to the site name", doc["name"], doc["short_name"])
	}
	if doc["description"] != "Ask #it-help before filing a ticket." {
		t.Errorf("description = %v, want the site description", doc["description"])
	}
	// The interface's own default neutral accent (color-utils.ts:
	// ACCENTS.neutral) — a fresh instance's install prompt is already
	// themed, not left white.
	if doc["theme_color"] != "#18181b" || doc["background_color"] != "#18181b" {
		t.Errorf("colours = %v / %v, want the built-in default", doc["theme_color"], doc["background_color"])
	}
	icons, _ := doc["icons"].([]any)
	if len(icons) != 1 {
		t.Fatalf("icons = %v, want exactly one", doc["icons"])
	}
	src, _ := icons[0].(map[string]any)["src"].(string)
	if src == "" || src == "/icon.png" {
		t.Errorf("icon src = %q, want the built-in inline mark", src)
	}
}
