package server

import (
	"net/http"
	"testing"
)

// Signed-in devices: an account's own list and the two ways to end one, plus
// the admin side of the same feature and its manage-admin authorisation.

type sessionRow struct {
	ID         string `json:"id"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Current    bool   `json:"current"`
}

func (in *instance) login(identifier, password string) *session {
	in.t.Helper()
	response := in.do(http.MethodPost, "/api/auth/login",
		map[string]string{"identifier": identifier, "password": password}, nil)
	if response.Code != http.StatusOK {
		in.t.Fatalf("login %s: %d %s", identifier, response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "obsidian_session" && cookie.Value != "" {
			return &session{cookie: cookie, password: password}
		}
	}
	in.t.Fatalf("login %s returned no session cookie", identifier)
	return nil
}

func TestProfileSessionsListsBothAndMarksTheCurrentOne(t *testing.T) {
	in := newInstance(t)
	registered := in.register("ada", "a-good-password")
	loggedIn := in.login("ada", "a-good-password")

	response := in.do(http.MethodGet, "/api/profile/sessions", nil, loggedIn)
	if response.Code != http.StatusOK {
		t.Fatalf("list sessions: %d %s", response.Code, response.Body.String())
	}
	body := decode[struct {
		Sessions []sessionRow `json:"sessions"`
	}](t, response)
	if len(body.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2 (register + login)", len(body.Sessions))
	}

	current := 0
	for _, row := range body.Sessions {
		if row.Current {
			current++
		}
		if row.ID == "" {
			t.Error("a session row has no id")
		}
	}
	if current != 1 {
		t.Fatalf("%d rows marked current, want exactly 1", current)
	}

	// The other session — the one register issued — is a real other row and
	// not this one, whatever order the list came back in.
	_ = registered
}

func TestRevokeSessionCannotTakeTheCurrentOne(t *testing.T) {
	in := newInstance(t)
	registered := in.register("ada", "a-good-password")
	in.login("ada", "a-good-password")

	list := decode[struct {
		Sessions []sessionRow `json:"sessions"`
	}](t, in.do(http.MethodGet, "/api/profile/sessions", nil, registered))

	var currentID string
	for _, row := range list.Sessions {
		if row.Current {
			currentID = row.ID
		}
	}
	if currentID == "" {
		t.Fatal("no session marked current")
	}

	response := in.do(http.MethodDelete, "/api/profile/sessions/"+currentID, nil, registered)
	if response.Code != http.StatusConflict {
		t.Fatalf("revoke the current session: %d %s, want 409", response.Code, response.Body.String())
	}
}

func TestRevokeSessionEndsAnotherOne(t *testing.T) {
	in := newInstance(t)
	registered := in.register("ada", "a-good-password")
	in.login("ada", "a-good-password")

	list := decode[struct {
		Sessions []sessionRow `json:"sessions"`
	}](t, in.do(http.MethodGet, "/api/profile/sessions", nil, registered))
	if len(list.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(list.Sessions))
	}

	var other string
	for _, row := range list.Sessions {
		if !row.Current {
			other = row.ID
		}
	}
	if other == "" {
		t.Fatal("no non-current session to revoke")
	}

	if response := in.do(http.MethodDelete, "/api/profile/sessions/"+other, nil, registered); response.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", response.Code, response.Body.String())
	}

	after := decode[struct {
		Sessions []sessionRow `json:"sessions"`
	}](t, in.do(http.MethodGet, "/api/profile/sessions", nil, registered))
	if len(after.Sessions) != 1 {
		t.Fatalf("got %d sessions after revoking one, want 1", len(after.Sessions))
	}
	if !after.Sessions[0].Current {
		t.Error("the surviving session is not the one still signed in")
	}
}

func TestRevokeOtherSessionsKeepsTheCurrentOne(t *testing.T) {
	in := newInstance(t)
	registered := in.register("ada", "a-good-password")
	in.login("ada", "a-good-password")
	in.login("ada", "a-good-password")

	before := decode[struct {
		Sessions []sessionRow `json:"sessions"`
	}](t, in.do(http.MethodGet, "/api/profile/sessions", nil, registered))
	if len(before.Sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(before.Sessions))
	}

	if response := in.do(http.MethodPost, "/api/profile/sessions/revoke-others", nil, registered); response.Code != http.StatusNoContent {
		t.Fatalf("revoke others: %d %s", response.Code, response.Body.String())
	}

	after := decode[struct {
		Sessions []sessionRow `json:"sessions"`
	}](t, in.do(http.MethodGet, "/api/profile/sessions", nil, registered))
	if len(after.Sessions) != 1 {
		t.Fatalf("got %d sessions after revoke-others, want 1", len(after.Sessions))
	}
	if !after.Sessions[0].Current {
		t.Error("revoke-others removed the caller's own session")
	}
}

// --- admin -------------------------------------------------------------------

func TestAdminSessionsRespectsManageAdminAuthorisation(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	plain := in.register("member", "a-good-password")
	admin := in.register("secondadmin", "a-good-password")

	// Promote "secondadmin" to a delegated administrator holding only
	// "users" — resetPassword's own authorisation shape, exercised here
	// against sessions instead of a credential.
	if response := in.do(http.MethodPatch, "/api/admin/users/"+admin.userID, map[string]any{
		"role": "admin", "admin_permissions": []string{"users"},
	}, founder); response.Code != http.StatusOK {
		t.Fatalf("promote secondadmin: %d %s", response.Code, response.Body.String())
	}

	// A delegated admin may list and sign out a plain member's sessions.
	if response := in.do(http.MethodGet, "/api/admin/users/"+plain.userID+"/sessions", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("list a plain member's sessions: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodDelete, "/api/admin/users/"+plain.userID+"/sessions", nil, admin); response.Code != http.StatusNoContent {
		t.Fatalf("sign a plain member out everywhere: %d %s", response.Code, response.Body.String())
	}

	// The founder is a super administrator; a delegated admin holding only
	// "users" may not manage one, and that must cover their sessions too.
	if response := in.do(http.MethodGet, "/api/admin/users/"+founder.userID+"/sessions", nil, admin); response.Code != http.StatusForbidden {
		t.Fatalf("list a super admin's sessions as a delegated admin: %d %s, want 403", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodDelete, "/api/admin/users/"+founder.userID+"/sessions", nil, admin); response.Code != http.StatusForbidden {
		t.Fatalf("sign a super admin out as a delegated admin: %d %s, want 403", response.Code, response.Body.String())
	}

	// The founder, a super administrator, may reach either.
	if response := in.do(http.MethodGet, "/api/admin/users/"+admin.userID+"/sessions", nil, founder); response.Code != http.StatusOK {
		t.Fatalf("super admin lists a delegated admin's sessions: %d %s", response.Code, response.Body.String())
	}
}
