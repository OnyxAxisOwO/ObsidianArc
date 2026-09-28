package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestInstanceBackupCommandsAreSuperAdminOnlyAndNeverAcceptKeys(t *testing.T) {
	delegated := user.User{Role: user.RoleAdmin, AdminPermissions: []string{"settings", "security", "availability"}}
	super := user.User{Role: user.RoleSuperAdmin}
	claimed := map[string]bool{}
	for _, cmd := range allCommands {
		for _, endpoint := range cmd.Endpoints {
			if !strings.Contains(endpoint, "/api/admin/backup") {
				continue
			}
			claimed[endpoint] = true
			if cmd.Permission != "super_admin" {
				t.Errorf("%s declares permission %q, want super_admin", cmd.Name, cmd.Permission)
			}
			if hasPermission(delegated, cmd.Permission) {
				t.Errorf("delegated settings admin can access %s", cmd.Name)
			}
			if !hasPermission(super, cmd.Permission) {
				t.Errorf("super admin cannot access %s", cmd.Name)
			}
			for _, flag := range cmd.Flags {
				name := strings.ToLower(flag.Name)
				if strings.Contains(name, "access-key") || strings.Contains(name, "secret-key") || strings.Contains(name, "secret-access") {
					t.Errorf("%s accepts credential flag %s, which would remain in local terminal history", cmd.Name, flag.Name)
				}
			}
		}
	}
	for _, route := range []string{
		"GET /api/admin/backup", "PUT /api/admin/backup",
		"POST /api/admin/backup/test", "POST /api/admin/backup/run",
	} {
		if !claimed[route] {
			t.Errorf("instance backup route %q is not available from the console", route)
		}
	}
}
