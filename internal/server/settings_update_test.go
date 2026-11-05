package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
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

	// The numeric fields share validation on both settings endpoints.
	bad := map[string]string{
		settings.ThemeWallpaperDim: "101", settings.HealthDisableBelow: "101",
		settings.HealthWindowMins: "0", settings.HealthRetainDays: "3651",
		settings.ConversationMaxTurns: "0", settings.QuotaMaxConcurrent: "-1",
	}
	for key, value := range bad {
		res := in.do(http.MethodPut, "/api/admin/settings", map[string]string{key: value}, admin)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("save %s=%s: %d %s", key, value, res.Code, res.Body.String())
		}
	}
	imported := in.do(http.MethodPost, "/api/admin/settings/import", bad, admin)
	skipped := decode[struct {
		Skipped []string `json:"skipped"`
	}](t, imported).Skipped
	if imported.Code != http.StatusOK || len(skipped) != len(bad) {
		t.Fatalf("invalid settings import: %d %s", imported.Code, imported.Body.String())
	}
	for _, key := range skipped {
		if _, ok := bad[key]; !ok {
			t.Fatalf("unexpected skipped key: %q", key)
		}
	}
	for _, values := range []map[string]string{
		{settings.ThemeWallpaperDim: "1", settings.HealthWindowMins: "1", settings.QuotaMaxConcurrent: "0"},
		{settings.ThemeWallpaperDim: "100", settings.HealthDisableBelow: "100", settings.HealthRetainDays: "3650"},
	} {
		if res := in.do(http.MethodPut, "/api/admin/settings", values, admin); res.Code != http.StatusOK {
			t.Fatalf("valid settings %v: %d %s", values, res.Code, res.Body.String())
		}
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

// Archiving is a switch an administrator turns off for everyone who is not an
// administrator. The settings write, the site response members read and the
// refusal on the archive itself have to agree, or the switch looks on while
// it refuses, or looks off while it lets people through.
func TestArchiveSettingCanBeTurnedOff(t *testing.T) {
	in := newInstance(t)
	ctx := context.Background()
	admin := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")

	adminChat, err := conversation.NewStore(in.db).Create(ctx, nil, admin.userID, conversation.NewConversation{Title: "Admin"})
	if err != nil {
		t.Fatal(err)
	}
	memberChat, err := conversation.NewStore(in.db).Create(ctx, nil, member.userID, conversation.NewConversation{Title: "Member"})
	if err != nil {
		t.Fatal(err)
	}
	archive := map[string]any{"archived": true}
	path := func(id string) string { return "/api/conversations/" + id }

	if response := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{settings.AllowArchive: "false"}, admin); response.Code != http.StatusOK {
		t.Fatalf("turn archiving off: %d %s", response.Code, response.Body.String())
	}

	saved := decode[struct {
		Settings map[string]string `json:"settings"`
	}](t, in.do(http.MethodGet, "/api/admin/settings", nil, admin)).Settings
	if saved[settings.AllowArchive] != "false" {
		t.Errorf("GET %s = %q, want false", settings.AllowArchive, saved[settings.AllowArchive])
	}

	site := decode[map[string]any](t, in.do(http.MethodGet, "/api/site", nil, nil))
	if site["allow_archive_conversations"] != false {
		t.Errorf("site allow_archive_conversations = %v, want false", site["allow_archive_conversations"])
	}

	refused := in.do(http.MethodPatch, path(memberChat.ID), archive, member)
	if refused.Code != http.StatusForbidden || errCode(t, refused) != "archive_not_permitted" {
		t.Fatalf("member archive while off: %d %s", refused.Code, refused.Body.String())
	}
	if allowed := in.do(http.MethodPatch, path(adminChat.ID), archive, admin); allowed.Code != http.StatusOK {
		t.Fatalf("administrator archive while off: %d %s", allowed.Code, allowed.Body.String())
	}

	if response := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{settings.AllowArchive: "true"}, admin); response.Code != http.StatusOK {
		t.Fatalf("turn archiving back on: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPatch, path(memberChat.ID), archive, member); response.Code != http.StatusOK {
		t.Fatalf("member archive after turning it back on: %d %s", response.Code, response.Body.String())
	}
}
