package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestAdministratorPageGrants(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	pages := map[string]string{
		"dashboard": "/dashboard", "users": "/users", "groups": "/groups",
		"providers": "/providers", "models": "/models", "availability": "/health",
		"usage": "/usage", "resources": "/resources", "codes": "/codes",
		"logs": "/logs", "security": "/security/events", "settings": "/settings",
		"announcements": "/announcements", "feedback": "/feedback",
		"invites": "/invites",
	}
	for _, grant := range user.AdminPermissions {
		t.Run(grant, func(t *testing.T) {
			response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
				map[string]any{"role": "admin", "admin_permissions": []string{grant}}, founder)
			if response.Code != http.StatusOK {
				t.Fatalf("grant: %d %s", response.Code, response.Body.String())
			}
			for page, path := range pages {
				response = in.do(http.MethodGet, "/api/admin"+path, nil, operator)
				allowed := page == grant || (page == "settings" && (grant == "security" || grant == "availability" || grant == "invites" || grant == "leaderboard"))
				if allowed {
					if response.Code != http.StatusOK {
						t.Errorf("%s: %d %s", page, response.Code, response.Body.String())
					}
				} else if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "admin_permission_denied") {
					t.Errorf("ungranted %s: %d %s", page, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestDelegatedAdministratorCannotIncreaseAuthority(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	other := in.register("other", "a-good-password")
	grant := func(target *session, role string, permissions []string, actor *session, status int) {
		t.Helper()
		res := in.do(http.MethodPatch, "/api/admin/users/"+target.userID,
			map[string]any{"role": role, "admin_permissions": permissions}, actor)
		if res.Code != status {
			t.Fatalf("grant %s as %s: %d %s", target.userID, actor.userID, res.Code, res.Body.String())
		}
	}
	grant(operator, "admin", []string{"administrators", "users", "groups"}, founder, http.StatusOK)
	grant(other, "admin", []string{"users"}, operator, http.StatusOK)
	grant(other, "admin", []string{"settings"}, operator, http.StatusForbidden)
	grant(other, "super_admin", []string{}, operator, http.StatusForbidden)
	grant(operator, "admin", []string{"users"}, operator, http.StatusForbidden)
	grant(founder, "user", []string{}, operator, http.StatusForbidden)
	grant(founder, "user", []string{}, founder, http.StatusConflict)
	grant(other, "admin", []string{"unknown"}, founder, http.StatusBadRequest)
	for _, operation := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/admin/users/" + founder.userID, map[string]any{"email": "changed@example.com"}},
		{http.MethodDelete, "/api/admin/users/" + founder.userID, nil},
		{http.MethodPost, "/api/admin/users/" + founder.userID + "/password", map[string]any{"new_password": "replacement-password"}},
	} {
		res := in.do(operation.method, operation.path, operation.body, operator)
		if res.Code != http.StatusForbidden {
			t.Fatalf("protected administrator: %d %s", res.Code, res.Body.String())
		}
	}
	grant(operator, "admin", []string{"administrators"}, founder, http.StatusOK)
	res := in.do(http.MethodPatch, "/api/admin/users/"+other.userID, map[string]any{"nickname": "changed"}, operator)
	if res.Code != http.StatusForbidden {
		t.Fatalf("operator without user-page access edited a profile: %d", res.Code)
	}
	grant(other, "admin", []string{}, operator, http.StatusForbidden)
	res = in.do(http.MethodGet, "/api/admin/users", nil, operator)
	if res.Code != http.StatusForbidden {
		t.Fatalf("revoked permission still usable: %d", res.Code)
	}
}

func TestSettingsGrantsAreScopedBySection(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	for _, grant := range []string{"security", "availability", "settings"} {
		res := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
			map[string]any{"role": "admin", "admin_permissions": []string{grant}}, founder)
		if res.Code != http.StatusOK {
			t.Fatal(res.Body.String())
		}
		for key, section := range map[string]string{"site.name": "settings", "registration.enabled": "security", "health.probe": "availability"} {
			value := "false"
			if key == "site.name" {
				value = "Arc"
			}
			res = in.do(http.MethodPut, "/api/admin/settings", map[string]string{key: value}, operator)
			want := http.StatusForbidden
			if section == grant {
				want = http.StatusOK
			}
			if res.Code != want {
				t.Fatalf("%s writes %s: %d %s", grant, key, res.Code, res.Body.String())
			}
		}
		res = in.do(http.MethodGet, "/api/admin/settings", nil, operator)
		payload := decode[struct {
			Settings map[string]string `json:"settings"`
		}](t, res)
		for key := range payload.Settings {
			section := "settings"
			if strings.HasPrefix(key, "health.") {
				section = "availability"
			}
			// Spelled out rather than borrowed from admin.settingPermission,
			// which is unexported: this is the claim that the mapping is
			// what it says it is, so it has to be written independently of
			// the thing it checks.
			if strings.HasPrefix(key, "registration.") || strings.HasPrefix(key, "turnstile.") ||
				strings.HasPrefix(key, "security.") || strings.HasPrefix(key, "oauth.") {
				section = "security"
			}
			if section != grant {
				t.Errorf("%s received %s", grant, key)
			}
		}
	}
}

func TestUserRoleWritesRequireAdministratorActionGrant(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	setGrants := func(permissions []string) {
		t.Helper()
		res := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
			map[string]any{"role": "admin", "admin_permissions": permissions}, founder)
		if res.Code != http.StatusOK {
			t.Fatal(res.Body.String())
		}
	}
	setGrants([]string{"users"})
	res := in.do(http.MethodPatch, "/api/admin/users/"+member.userID, map[string]string{"nickname": "Updated"}, operator)
	if res.Code != http.StatusOK {
		t.Fatalf("ordinary profile edit: %d %s", res.Code, res.Body.String())
	}
	for _, body := range []map[string]any{
		{"role": "admin"}, {"role": "user"}, {"role": "super_admin"}, {"admin_permissions": []string{"users"}},
	} {
		res = in.do(http.MethodPatch, "/api/admin/users/"+member.userID, body, operator)
		if res.Code != http.StatusForbidden {
			t.Fatalf("role write without action grant: %d %s", res.Code, res.Body.String())
		}
	}
	setGrants([]string{"users", "administrators"})
	res = in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"users"}}, operator)
	if res.Code != http.StatusOK {
		t.Fatalf("role write with action grant: %d %s", res.Code, res.Body.String())
	}
	changed := decode[struct {
		User user.User `json:"user"`
	}](t, res)
	if changed.User.Role != user.RoleAdmin || !changed.User.CanAdmin("users") {
		t.Fatalf("role/grants were not saved: %+v", changed.User)
	}
	setGrants([]string{"users"})
	res = in.do(http.MethodPatch, "/api/admin/users/"+member.userID, map[string]string{"role": "user"}, operator)
	if res.Code != http.StatusForbidden {
		t.Fatalf("revoked action grant still works: %d", res.Code)
	}
}

func TestAdminUserPagesReachEveryAccount(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	store := user.NewStore(in.db)
	for i := 0; i < 65; i++ {
		if _, err := store.Create(context.Background(), nil, user.CreateInput{Username: fmt.Sprintf("page-%02d", i), PasswordHash: "unused"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []string{"users", "member-options"} {
		seen := map[string]bool{}
		for offset := 0; offset < 65; offset += 20 {
			res := in.do(http.MethodGet, fmt.Sprintf("/api/admin/%s?q=page-&limit=20&offset=%d", endpoint, offset), nil, founder)
			if res.Code != http.StatusOK {
				t.Fatal(res.Body.String())
			}
			payload := decode[struct {
				Users []user.User `json:"users"`
				Total int         `json:"total"`
			}](t, res)
			if payload.Total != 65 {
				t.Fatalf("%s total = %d", endpoint, payload.Total)
			}
			for _, account := range payload.Users {
				if seen[account.ID] {
					t.Fatalf("%s duplicate page row %s", endpoint, account.ID)
				}
				seen[account.ID] = true
			}
		}
		if len(seen) != 65 {
			t.Fatalf("%s only reached %d accounts", endpoint, len(seen))
		}
	}
}

// The invites page saves through the shared settings route. An operator
// granted invites alone reaches the invite settings and the one registration
// switch the page's mode select writes — and not a key belonging to any other
// page, which the route letting them in must not be mistaken for.
func TestAnInvitesGrantReachesItsOwnSettingsOnly(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"invites"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", response.Code, response.Body.String())
	}

	own := map[string]string{"registration.enabled": "true", "invites.required": "true", "invites.reward_cards": "2"}
	if response := in.do(http.MethodPut, "/api/admin/settings", own, operator); response.Code != http.StatusOK {
		t.Fatalf("own settings: %d %s", response.Code, response.Body.String())
	}
	for _, key := range []string{"site.name", "security.two_factor_policy", "registration.require_email"} {
		response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{key: "x"}, operator)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s: %d %s", key, response.Code, response.Body.String())
		}
	}
	listed := in.do(http.MethodGet, "/api/admin/settings", nil, operator)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"invites.required":"true"`) ||
		strings.Contains(listed.Body.String(), `"site.name"`) {
		t.Fatalf("listed: %d %s", listed.Code, listed.Body.String())
	}
}
