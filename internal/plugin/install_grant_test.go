package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// vaultKey is a setting a built-in plugin declares under the security grant, as
// it would declare a callback address for its own use.
const vaultKey = "vault.callback"

var defineVault sync.Once

func defineVaultSetting() {
	defineVault.Do(func() {
		settings.Define(settings.Definition{Key: vaultKey, Plugin: "vault", Permission: "security"})
	})
}

// grantedAccount is an administrator who holds exactly the grants given.
func grantedAccount(t *testing.T, r *rig, name string, grants ...string) user.User {
	t.Helper()
	account := accountOf(t, r, name, user.RoleAdmin)
	account, err := r.users.UpdateAdminFields(context.Background(), nil, account.ID, user.AdminUpdate{AdminPermissions: &grants})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

// A built-in plugin's install writes its settings under each key's own grant,
// as the shared settings route does. plugins_manage lets its holder install
// plugins, but it does not let them set a key the security grant owns.
func TestInstallingAPluginSetsOnlyTheKeysTheActorMayWrite(t *testing.T) {
	defineVaultSetting()
	r := newRig(t, fake{name: "vault", setup: noSetup}, fake{name: "plain", setup: noSetup})
	h := NewHandlers(r.manager, nil, r.settings, nil)
	manager := grantedAccount(t, r, "manager", PermissionManage)
	security := grantedAccount(t, r, "security", PermissionManage, "security")

	install := func(actor user.User, name string, values map[string]string) error {
		return installAs(t, h, actor, name, values)
	}

	err := install(manager, "vault", map[string]string{vaultKey: "https://elsewhere.example/hook"})
	var refusal *httpx.Error
	if !errors.As(err, &refusal) || refusal.Status != http.StatusForbidden || refusal.Code != "admin_permission_denied" {
		t.Fatalf("a plugins_manage holder set a key it may not write: %v", err)
	}
	if r.manager.State("vault") != StateAvailable {
		t.Fatalf("a refused install changed the plugin to %s", r.manager.State("vault"))
	}
	var stored int
	if err := r.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM settings WHERE key = ?`, vaultKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatal("a refused install wrote the key anyway")
	}

	// The grant is per key: the same holder installs a plugin that sets nothing
	// it may not write.
	if err := install(manager, "plain", nil); err != nil {
		t.Fatalf("an install with no restricted key: %v", err)
	}

	if err := install(security, "vault", map[string]string{vaultKey: "https://elsewhere.example/hook"}); err != nil {
		t.Fatalf("a holder of the key's grant: %v", err)
	}
	if got := r.settings.Get(vaultKey); got != "https://elsewhere.example/hook" || r.manager.State("vault") != StateEnabled {
		t.Fatalf("after the install: setting %q, state %s", got, r.manager.State("vault"))
	}
}

// installAs sends an enabled install of name as actor, with the settings given,
// and answers what the handler answered.
func installAs(t *testing.T, h *Handlers, actor user.User, name string, values map[string]string) error {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"enable": true, "settings": values})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/plugins/"+name+"/install", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("name", name)
	req = req.WithContext(auth.WithUser(req.Context(), actor))
	return h.install(httptest.NewRecorder(), req)
}

// The install dialog sends a delegate only the keys it may write. A delegate who
// never touched the restricted key installs the plugin and the key stays unset;
// the same key sent by that delegate is still refused, and nothing is written.
func TestADelegateInstallsWithTheRestrictedKeyLeftOut(t *testing.T) {
	defineVaultSetting()
	r := newRig(t, fake{name: "vault", setup: noSetup})
	h := NewHandlers(r.manager, nil, r.settings, nil)
	manager := grantedAccount(t, r, "manager", PermissionManage)
	ctx := context.Background()
	storedKeys := func() int {
		t.Helper()
		var n int
		if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM settings WHERE key = ?`, vaultKey).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	err := installAs(t, h, manager, "vault", map[string]string{vaultKey: "https://elsewhere.example/hook"})
	var refusal *httpx.Error
	if !errors.As(err, &refusal) || refusal.Status != http.StatusForbidden || refusal.Code != "admin_permission_denied" {
		t.Fatalf("a delegate without the grant sent the restricted key: %v", err)
	}
	if r.manager.State("vault") != StateAvailable || storedKeys() != 0 {
		t.Fatalf("a refused install changed the plugin to %s or stored the key", r.manager.State("vault"))
	}

	if err := installAs(t, h, manager, "vault", map[string]string{}); err != nil {
		t.Fatalf("an install that leaves the restricted key out: %v", err)
	}
	if got := r.manager.State("vault"); got != StateEnabled {
		t.Fatalf("after the install the plugin is %s", got)
	}
	if n := storedKeys(); n != 0 {
		t.Fatalf("an install that left the restricted key out stored it (%d rows)", n)
	}
}

// The list and the detail name the keys each viewer may write, so the install
// dialog can offer only those. A delegate without the grant is offered none of
// the restricted key, and never a null where the list should be.
func TestThePluginListAndDetailNameTheKeysTheViewerMayWrite(t *testing.T) {
	defineVaultSetting()
	r := newRig(t, fake{name: "vault", setup: noSetup}, fake{name: "plain", setup: noSetup})
	h := NewHandlers(r.manager, nil, r.settings, nil)
	manager := grantedAccount(t, r, "manager", PermissionManage)
	security := grantedAccount(t, r, "security", PermissionManage, "security")

	listed := func(actor user.User) map[string][]string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/plugins", nil)
		req = req.WithContext(auth.WithUser(req.Context(), actor))
		rec := httptest.NewRecorder()
		if err := h.list(rec, req); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Plugins []struct {
				Name             string   `json:"name"`
				WritableSettings []string `json:"writable_settings"`
			} `json:"plugins"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for _, p := range body.Plugins {
			if p.WritableSettings == nil {
				t.Fatalf("the list carries no writable_settings for %s", p.Name)
			}
			out[p.Name] = p.WritableSettings
		}
		return out
	}
	shown := func(actor user.User) []string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/plugins/vault", nil)
		req.SetPathValue("name", "vault")
		req = req.WithContext(auth.WithUser(req.Context(), actor))
		rec := httptest.NewRecorder()
		if err := h.show(rec, req); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Plugin struct {
				WritableSettings []string `json:"writable_settings"`
			} `json:"plugin"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Plugin.WritableSettings
	}

	if got := listed(manager)["vault"]; len(got) != 0 {
		t.Fatalf("a delegate without the grant is offered %v", got)
	}
	if got := listed(security)["vault"]; len(got) != 1 || got[0] != vaultKey {
		t.Fatalf("a holder of the grant is offered %v", got)
	}
	if got := shown(manager); len(got) != 0 {
		t.Fatalf("the detail offers a delegate without the grant %v", got)
	}
	if got := shown(security); len(got) != 1 || got[0] != vaultKey {
		t.Fatalf("the detail offers a holder of the grant %v", got)
	}
}
