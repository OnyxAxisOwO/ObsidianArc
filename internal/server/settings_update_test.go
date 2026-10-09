package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

func TestSettingsFormEnablesAPIOnFreshDatabase(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	// The mounted Vue form asserts this same payload, so either side drifting fails a test.
	document, err := os.ReadFile("../../web/test/fixtures/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	if err := json.Unmarshal(document, &body); err != nil {
		t.Fatal(err)
	}
	before := in.do(http.MethodGet, "/api/admin/settings", nil, admin)
	if before.Code != http.StatusOK {
		t.Fatalf("load defaults: %d %s", before.Code, before.Body.String())
	}
	defaults := decode[struct {
		Settings map[string]string `json:"settings"`
	}](t, before).Settings
	for key, value := range body {
		if key == settings.APIEnabled {
			value = "false"
		}
		if defaults[key] != value {
			t.Fatalf("fresh %s = %q, want %q", key, defaults[key], value)
		}
	}

	for _, size := range []string{"8", "12"} {
		body[settings.ThemeWallpaperDim] = size
		response := in.do(http.MethodPut, "/api/admin/settings", body, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("save form with size %s: %d %s", size, response.Code, response.Body.String())
		}
		response = in.do(http.MethodGet, "/api/admin/settings", nil, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("reload settings: %d %s", response.Code, response.Body.String())
		}
		saved := decode[struct {
			Settings map[string]string `json:"settings"`
		}](t, response).Settings
		reloaded := settings.New(in.db)
		if err := reloaded.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		for key, want := range body {
			if got := saved[key]; got != want {
				t.Errorf("GET %s = %q, want %q", key, got, want)
			}
			if got := reloaded.Get(key); got != want {
				t.Errorf("persisted %s = %q, want %q", key, got, want)
			}
		}
	}
}

func TestImportSettingsKeepsANumericSetting(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	response := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		settings.ThemeWallpaperDim: "12",
	}, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	result := decode[struct {
		Applied  int               `json:"applied"`
		Skipped  []string          `json:"skipped"`
		Settings map[string]string `json:"settings"`
	}](t, response)
	if result.Applied != 1 || len(result.Skipped) != 0 || result.Settings[settings.ThemeWallpaperDim] != "12" {
		t.Fatalf("imported size were not applied: %+v", result)
	}
	reloaded := settings.New(in.db)
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get(settings.ThemeWallpaperDim); got != "12" {
		t.Fatalf("persisted size = %q, want 12", got)
	}
}

// The form offers the same range; that is a convenience, and this is the
// check. The wallpaper dim stands in for every bounded number here, because
// it is one the plain settings grant may write.
func TestNumericSettingsAreBounded(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	for _, value := range []string{"-5", "abc", "101", "99999", ""} {
		response := in.do(http.MethodPut, "/api/admin/settings",
			map[string]string{settings.ThemeWallpaperDim: value}, admin)
		if response.Code != http.StatusBadRequest {
			t.Errorf("size %q: %d %s, want 400", value, response.Code, response.Body.String())
		}
		// Refused whole, as any other rejected key is: a bad value must not
		// land while the request that carried it fails.
		reloaded := settings.New(in.db)
		if err := reloaded.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := reloaded.Get(settings.ThemeWallpaperDim); got != "0" {
			t.Fatalf("size %q was stored as %q despite the refusal", value, got)
		}
	}

	// The ends of the range belong to the operator, not to the check.
	for _, value := range []string{"1", "100"} {
		response := in.do(http.MethodPut, "/api/admin/settings",
			map[string]string{settings.ThemeWallpaperDim: value}, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("size %q: %d %s", value, response.Code, response.Body.String())
		}
		reloaded := settings.New(in.db)
		if err := reloaded.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := reloaded.Get(settings.ThemeWallpaperDim); got != value {
			t.Fatalf("persisted size = %q, want %q", got, value)
		}
	}
}

// An import is forgiving where a save refuses: a document from a newer
// release should stay usable. An out-of-range value is dropped and named
// rather than carried, so the instance keeps a number it can honour.
func TestImportSettingsDropsValuesOutOfRange(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	response := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{
		settings.ThemeWallpaperDim: "99999",
		settings.SiteName:          "Carried Over",
	}, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	result := decode[struct {
		Applied int      `json:"applied"`
		Skipped []string `json:"skipped"`
	}](t, response)
	if len(result.Skipped) != 1 || result.Skipped[0] != settings.ThemeWallpaperDim {
		t.Errorf("skipped = %v, want just the dim", result.Skipped)
	}
	reloaded := settings.New(in.db)
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get(settings.ThemeWallpaperDim); got != "0" {
		t.Errorf("size = %q, want the default kept", got)
	}
	// One refused value must not cost the operator the rest of the document.
	if got := reloaded.Get(settings.SiteName); got != "Carried Over" {
		t.Errorf("site.name = %q, want the imported value", got)
	}
}

func TestSettingsRejectUnknownKeysWithoutWriting(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	before := in.do(http.MethodGet, "/api/admin/settings", nil, admin)
	if before.Code != http.StatusOK {
		t.Fatal(before.Body.String())
	}
	values := decode[struct {
		Settings map[string]string `json:"settings"`
	}](t, before).Settings

	for _, key := range []string{"theme.wallpaper_dims", settings.AttachmentPurgeLast} {
		t.Run(key, func(t *testing.T) {
			response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
				settings.APIEnabled:        "true",
				settings.ThemeWallpaperDim: "12",
				key:                        "99",
			}, admin)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("unknown key: %d %s", response.Code, response.Body.String())
			}
			failure := decode[struct {
				Error struct{ Message string } `json:"error"`
			}](t, response)
			if !strings.Contains(failure.Error.Message, "Unknown setting") || !strings.Contains(failure.Error.Message, key) {
				t.Fatalf("wrong rejection: %s", response.Body.String())
			}
			response = in.do(http.MethodGet, "/api/admin/settings", nil, admin)
			if response.Code != http.StatusOK {
				t.Fatal(response.Body.String())
			}
			after := decode[struct {
				Settings map[string]string `json:"settings"`
			}](t, response).Settings
			if !reflect.DeepEqual(after, values) {
				t.Fatal("rejected update changed visible settings")
			}
			reloaded := settings.New(in.db)
			if err := reloaded.Load(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reloaded.All(), values) {
				t.Fatal("rejected update changed persisted settings")
			}
		})
	}
}

func TestNumericSettingRequiresSettingsPermission(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	for _, grant := range []string{"settings", "security", "availability"} {
		response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
			map[string]any{"role": "admin", "admin_permissions": []string{grant}}, founder)
		if response.Code != http.StatusOK {
			t.Fatalf("grant %s: %d %s", grant, response.Code, response.Body.String())
		}
		want := http.StatusForbidden
		if grant == "settings" {
			want = http.StatusOK
		}
		for _, endpoint := range []struct{ method, path string }{
			{http.MethodPut, "/api/admin/settings"},
			{http.MethodPost, "/api/admin/settings/import"},
		} {
			response = in.do(endpoint.method, endpoint.path, map[string]string{settings.ThemeWallpaperDim: "12"}, operator)
			if response.Code != want {
				t.Fatalf("%s writes %s: %d %s", grant, endpoint.path, response.Code, response.Body.String())
			}
		}
	}
}
