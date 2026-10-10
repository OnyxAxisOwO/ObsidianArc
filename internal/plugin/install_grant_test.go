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
	h := NewHandlers(r.manager, nil, nil, nil)
	manager := grantedAccount(t, r, "manager", PermissionManage)
	security := grantedAccount(t, r, "security", PermissionManage, "security")

	install := func(actor user.User, name string, values map[string]string) error {
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
