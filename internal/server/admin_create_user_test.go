package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// createUser skips the registration switch and the signup limits on purpose,
// which makes it the shortest path to an account on the instance. What it must
// not skip is the escalation guard: an operator delegated "users" could
// otherwise mint themselves authority nobody gave them.
func TestAdminCreateUserCannotExceedTheCreatorsAuthority(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")

	promote := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"administrators", "users"}}, founder)
	if promote.Code != http.StatusOK {
		t.Fatalf("promote operator: %d %s", promote.Code, promote.Body.String())
	}

	create := func(name string, body map[string]any, as *session) int {
		t.Helper()
		body["username"] = name
		return in.do(http.MethodPost, "/api/admin/users", body, as).Code
	}

	cases := []struct {
		name string
		who  string
		body map[string]any
		as   *session
		want int
	}{
		{"一个普通账户", "plain-one", map[string]any{"password": "a-good-password"}, operator, http.StatusCreated},
		{"授予自己有的权限", "helper-one", map[string]any{"role": "admin", "admin_permissions": []string{"users"}}, operator, http.StatusCreated},
		{"授予自己没有的权限", "helper-two", map[string]any{"role": "admin", "admin_permissions": []string{"settings"}}, operator, http.StatusForbidden},
		{"直接造一个超管", "helper-three", map[string]any{"role": "super_admin"}, operator, http.StatusForbidden},
		{"超管可以造超管", "second-founder", map[string]any{"role": "super_admin"}, founder, http.StatusCreated},
		{"不存在的权限", "helper-four", map[string]any{"role": "admin", "admin_permissions": []string{"nope"}}, founder, http.StatusBadRequest},
		{"用户名重复", "founder", map[string]any{"password": "a-good-password"}, founder, http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := create(c.who, c.body, c.as); got != c.want {
				t.Fatalf("%s: got %d, want %d", c.name, got, c.want)
			}
		})
	}
}

// Grants only mean anything on an administrator. Leaving them attached to a
// plain account is a trap for whoever promotes it later and does not think to
// look at a list that was never supposed to be there.
func TestAdminCreateUserDropsGrantsOnNonAdmins(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	response := in.do(http.MethodPost, "/api/admin/users", map[string]any{
		"username": "plain-with-grants", "role": "user", "admin_permissions": []string{"users"},
	}, founder)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}

	var payload struct {
		User struct {
			ID               string   `json:"id"`
			Role             string   `json:"role"`
			AdminPermissions []string `json:"admin_permissions"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.User.Role != "user" {
		t.Fatalf("role: got %q", payload.User.Role)
	}
	if len(payload.User.AdminPermissions) != 0 {
		t.Fatalf("grants survived on a plain account: %v", payload.User.AdminPermissions)
	}

	// And the stored row agrees with what was returned.
	stored := in.do(http.MethodGet, "/api/admin/users/"+payload.User.ID, nil, founder)
	if stored.Code != http.StatusOK {
		t.Fatalf("read back: %d %s", stored.Code, stored.Body.String())
	}
	var readBack struct {
		User struct {
			AdminPermissions []string `json:"admin_permissions"`
		} `json:"user"`
	}
	if err := json.Unmarshal(stored.Body.Bytes(), &readBack); err != nil {
		t.Fatalf("decode read back: %v", err)
	}
	if len(readBack.User.AdminPermissions) != 0 {
		t.Fatalf("grants stored on a plain account: %v", readBack.User.AdminPermissions)
	}
}

// An account created without a password has none, and the login form must not
// treat "no password" as "any password will do". This is the whole basis for
// calling it a safe shape for a service account.
func TestAdminCreatedAccountWithoutPasswordCannotSignIn(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	response := in.do(http.MethodPost, "/api/admin/users",
		map[string]any{"username": "station-bot"}, founder)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}

	for _, attempt := range []string{"", "a-good-password", " "} {
		login := in.do(http.MethodPost, "/api/auth/login",
			map[string]string{"username": "station-bot", "password": attempt}, nil)
		if login.Code == http.StatusOK {
			t.Fatalf("signed in to a passwordless account with %q", attempt)
		}
	}
}

// A name the signup form refuses must be refused the same way here. The admin
// route answered 500 for it: the translation had no case for the username
// sentinel, so it fell through to the internal-error default.
func TestAdminCreateUserRejectsAnInvalidUsername(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	for _, name := range []string{"bad name!", "中文用户", "ab"} {
		response := in.do(http.MethodPost, "/api/admin/users",
			map[string]any{"username": name, "password": "a-good-password"}, founder)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("username %q: %d %s, want 400", name, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "Username must be 3-32 characters") {
			t.Fatalf("username %q: the body does not name the rule: %s", name, response.Body.String())
		}
	}
}
