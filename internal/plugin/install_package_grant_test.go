package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A package install writes its settings like a compiled-in install does, each
// under its key's own grant. The grant is checked before the upload token is
// looked at, so a delegate sending a restricted key is refused with the grant
// error even when the token is bogus; a holder of the grant gets past the check
// and meets the token error instead.
func TestInstallingAPackageSetsOnlyTheKeysTheActorMayWrite(t *testing.T) {
	defineVaultSetting()
	r := newRig(t, fake{name: "vault", setup: noSetup})
	h := NewHandlers(r.manager, nil, r.settings, nil)
	manager := grantedAccount(t, r, "manager", PermissionManage)
	security := grantedAccount(t, r, "security", PermissionManage, "security")

	installPackageAs := func(actor user.User) error {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"token": "no-such-upload", "enable": true,
			"settings": map[string]string{vaultKey: "https://elsewhere.example/hook"},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/plugins/packages/install", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(auth.WithUser(req.Context(), actor))
		return h.installPackage(httptest.NewRecorder(), req)
	}

	err := installPackageAs(manager)
	var refusal *httpx.Error
	if !errors.As(err, &refusal) || refusal.Status != http.StatusForbidden || refusal.Code != "admin_permission_denied" {
		t.Fatalf("a delegate without the grant sent a restricted key to a package install: %v", err)
	}

	// The token is not a real upload, so the holder's install fails at the
	// token step; what matters is that it does not fail at the grant.
	err = installPackageAs(security)
	if err == nil || (errors.As(err, &refusal) && refusal.Code == "admin_permission_denied") {
		t.Fatalf("a holder of the grant was refused at the grant check: %v", err)
	}
	if r.manager.State("vault") != StateAvailable {
		t.Fatalf("a refused package install changed the plugin to %s", r.manager.State("vault"))
	}
}
