package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Erasing a plugin's data runs its purge SQL as the database's owner, so only a
// super administrator may ask for it, as the install of a package is only theirs.
// A delegated remover may still uninstall a plugin and keep its data.
func TestOnlyASuperAdministratorMayPurgeAPlugin(t *testing.T) {
	r := newRig(t, newGadget())
	ctx := context.Background()
	if err := r.manager.Install(ctx, someone, "gadget", InstallOptions{Enable: true}); err != nil {
		t.Fatal(err)
	}
	h := NewHandlers(r.manager, nil, nil, nil)
	remover := grantedAccount(t, r, "remover", PermissionRemove)
	boss := accountOf(t, r, "boss", user.RoleSuperAdmin)

	uninstall := func(actor user.User, purge bool) error {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"purge": purge})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/plugins/gadget/uninstall", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("name", "gadget")
		req = req.WithContext(auth.WithUser(req.Context(), actor))
		return h.uninstall(httptest.NewRecorder(), req)
	}

	err := uninstall(remover, true)
	var refusal *httpx.Error
	if !errors.As(err, &refusal) || refusal.Status != http.StatusForbidden || refusal.Code != "admin_permission_denied" {
		t.Fatalf("a delegated remover purged a plugin: %v", err)
	}
	if !r.manager.Enabled("gadget") || !r.tableExists(t) {
		t.Fatal("a refused purge switched the plugin off or dropped its table")
	}

	if err := uninstall(remover, false); err != nil {
		t.Fatalf("a delegated removal that keeps the data: %v", err)
	}
	if !r.tableExists(t) {
		t.Fatal("a removal that keeps the data dropped the table")
	}

	if err := r.manager.Install(ctx, someone, "gadget", InstallOptions{Enable: true}); err != nil {
		t.Fatal(err)
	}
	if err := uninstall(boss, true); err != nil {
		t.Fatalf("a super administrator's purge: %v", err)
	}
	if r.tableExists(t) {
		t.Fatal("a super administrator's purge kept the table")
	}
}
