package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The delegated grants that reach past their own pages. The security grant
// runs the sign-in applications and the address mail links are built from; the
// invites grant can mint a code and must not be able to redeem it for itself;
// the dashboard grant reads the newest accounts. Each is held to what it should
// reach, and a super administrator is not.

func applicationIDOf(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[struct {
		Application struct {
			ID string `json:"id"`
		} `json:"application"`
	}](t, response).Application.ID
}

// Trust lets an application skip the consent screen for everybody who signs
// in. The security grant keeps every ordinary application and gives up the
// trusted ones: registering, changing, rotating or removing one needs the super
// administrator, and so does making an ordinary one trusted.
func TestASecurityAdministratorCannotChangeTrustedApplications(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	delegate(t, in, founder, operator, "security")

	refused := in.do(http.MethodPost, "/api/admin/applications", map[string]any{
		"name": "Their Service", "redirect_uris": "https://their.example.com/cb", "trusted": true,
	}, operator)
	if refused.Code != http.StatusForbidden || errCode(t, refused) != "super_admin_required" {
		t.Fatalf("a security administrator registered a trusted application: %d %s", refused.Code, refused.Body.String())
	}

	ordinary := in.do(http.MethodPost, "/api/admin/applications", map[string]any{
		"name": "Wiki", "redirect_uris": "https://wiki.example.com/cb",
	}, operator)
	if ordinary.Code != http.StatusCreated {
		t.Fatalf("a security administrator could not register an ordinary application: %d %s", ordinary.Code, ordinary.Body.String())
	}
	ordinaryPath := "/api/admin/applications/" + applicationIDOf(t, ordinary)

	own := in.do(http.MethodPost, "/api/admin/applications", map[string]any{
		"name": "Own Service", "redirect_uris": "https://own.example.com/cb", "trusted": true,
	}, founder)
	if own.Code != http.StatusCreated {
		t.Fatalf("the super administrator could not register a trusted application: %d %s", own.Code, own.Body.String())
	}
	trustedPath := "/api/admin/applications/" + applicationIDOf(t, own)

	type attempt struct {
		method, path string
		body         any
	}
	attempts := map[string]attempt{
		"rename":       {http.MethodPatch, trustedPath, map[string]any{"name": "Renamed"}},
		"callbacks":    {http.MethodPatch, trustedPath, map[string]any{"redirect_uris": "https://evil.example.net/cb"}},
		"untrust":      {http.MethodPatch, trustedPath, map[string]any{"trusted": false}},
		"rotate":       {http.MethodPost, trustedPath + "/secret", nil},
		"delete":       {http.MethodDelete, trustedPath, nil},
		"make trusted": {http.MethodPatch, ordinaryPath, map[string]any{"trusted": true}},
	}
	for label, try := range attempts {
		response := in.do(try.method, try.path, try.body, operator)
		if response.Code != http.StatusForbidden || errCode(t, response) != "super_admin_required" {
			t.Errorf("%s: a security administrator got %d %s", label, response.Code, response.Body.String())
		}
	}

	if response := in.do(http.MethodPatch, ordinaryPath, map[string]any{"redirect_uris": "https://wiki.example.org/cb"}, operator); response.Code != http.StatusOK {
		t.Errorf("changing an ordinary application's callbacks: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPost, ordinaryPath+"/secret", nil, operator); response.Code != http.StatusOK {
		t.Errorf("rotating an ordinary application's secret: %d %s", response.Code, response.Body.String())
	}

	if response := in.do(http.MethodPatch, trustedPath, map[string]any{"name": "Renamed"}, founder); response.Code != http.StatusOK {
		t.Errorf("the super administrator renaming a trusted application: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPost, trustedPath+"/secret", nil, founder); response.Code != http.StatusOK {
		t.Errorf("the super administrator rotating a trusted application's secret: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodDelete, trustedPath, nil, founder); response.Code != http.StatusNoContent {
		t.Errorf("the super administrator removing a trusted application: %d %s", response.Code, response.Body.String())
	}
}

// The address mailed links are built from decides where a person who clicks
// one ends up, so the security grant may not move it. It may still save the
// rest of the mail form, and may save the address as it already stands.
func TestOnlyASuperAdministratorMovesThePublicURL(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	delegate(t, in, founder, operator, "security")

	saved := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "127.0.0.1", "port": 1, "from": "arc@example.com",
		"public_url": "https://arc.example.com", "password": "mail-secret",
	}, founder)
	if saved.Code != http.StatusOK {
		t.Fatalf("super administrator saving mail: %d %s", saved.Code, saved.Body.String())
	}

	moved := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "127.0.0.1", "port": 1, "from": "arc@example.com", "public_url": "https://phish.example.net",
	}, operator)
	if moved.Code != http.StatusForbidden || errCode(t, moved) != "super_admin_required" {
		t.Fatalf("a security administrator moved the public URL: %d %s", moved.Code, moved.Body.String())
	}

	rest := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "127.0.0.1", "port": 1, "from": "ops@example.com", "public_url": "https://arc.example.com",
	}, operator)
	if rest.Code != http.StatusOK {
		t.Fatalf("a security administrator saving the rest of the mail form: %d %s", rest.Code, rest.Body.String())
	}

	if response := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "127.0.0.1", "port": 1, "from": "ops@example.com", "public_url": "https://arc2.example.com",
	}, founder); response.Code != http.StatusOK {
		t.Fatalf("super administrator moving the public URL: %d %s", response.Code, response.Body.String())
	}
	shown := in.do(http.MethodGet, "/api/admin/mail", nil, founder)
	if shown.Code != http.StatusOK {
		t.Fatalf("read mail: %d %s", shown.Code, shown.Body.String())
	}
	if got := decode[struct {
		PublicURL string `json:"public_url"`
	}](t, shown).PublicURL; got != "https://arc2.example.com" {
		t.Fatalf("public_url = %q after the super administrator's move", got)
	}
}

// The dashboard lists the newest accounts with the fields it can show. The
// address, the signup details and the rest of the record belong to the users
// grant, which the dashboard grant does not hold.
func TestDashboardGrantSeesNoAccountAddresses(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	viewer := in.register("viewer", "a-good-password")
	delegate(t, in, founder, viewer, "dashboard")
	registered := in.do(http.MethodPost, "/api/auth/register", map[string]string{
		"username": "newcomer", "password": "another-password", "email": "newcomer@example.com",
	}, nil)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", registered.Code, registered.Body.String())
	}

	response := in.do(http.MethodGet, "/api/admin/dashboard?metric=tokens&tz=0", nil, viewer)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "newcomer@example.com") {
		t.Fatal("the dashboard grant was sent an account's address")
	}
	dashboard := decode[struct {
		NewestUsers []map[string]any `json:"newest_users"`
	}](t, response)
	if len(dashboard.NewestUsers) == 0 {
		t.Fatal("the dashboard lists no newest users")
	}
	allowed := map[string]bool{"id": true, "username": true, "nickname": true, "created_at": true}
	for _, account := range dashboard.NewestUsers {
		for key := range account {
			if !allowed[key] {
				t.Errorf("newest_users carries %q, which the dashboard does not show", key)
			}
		}
	}
}

// The creator of an invite code already holds it. Claiming it onto their own
// account would give them a group that the groups grant is there to decide,
// and an invites grant alone must not be able to do that.
func TestClaimEndpointRefusesTheCodesCreator(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	delegate(t, in, founder, operator, "invites")

	group := decode[struct {
		Group struct {
			ID string `json:"id"`
		} `json:"group"`
	}](t, in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Operator Trial"}, founder))

	created := in.do(http.MethodPost, "/api/admin/invites", map[string]any{
		"kind": "partner", "count": 1, "code": "SELFMADE", "name": "Self Partner",
		"group_id": group.Group.ID, "group_days": 9,
	}, operator)
	if created.Code != http.StatusCreated {
		t.Fatalf("invites grant creating a partner code: %d %s", created.Code, created.Body.String())
	}

	claim := in.do(http.MethodPost, "/api/profile/invites/claim", map[string]string{"code": "SELFMADE"}, operator)
	if claim.Code != http.StatusForbidden || errCode(t, claim) != "invite_own_code" {
		t.Fatalf("the creator claimed their own code: %d %s", claim.Code, claim.Body.String())
	}

	member := in.register("member", "another-password")
	if response := in.do(http.MethodPost, "/api/profile/invites/claim", map[string]string{"code": "SELFMADE"}, member); response.Code != http.StatusOK {
		t.Fatalf("another account claiming the same code: %d %s", response.Code, response.Body.String())
	}
}
