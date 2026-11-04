package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
// in, and a callback is where its codes are sent. The security grant keeps the
// ordinary applications it can register, rename, rotate or disable, but not the
// trusted ones and not the callbacks of any application: those need the super
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

	if response := in.do(http.MethodPatch, ordinaryPath, map[string]any{"redirect_uris": "https://wiki.example.org/cb"}, operator); response.Code != http.StatusForbidden || errCode(t, response) != "super_admin_required" {
		t.Errorf("a security administrator changed an ordinary application's callbacks: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPatch, ordinaryPath, map[string]any{"redirect_uris": "https://wiki.example.com/cb"}, operator); response.Code != http.StatusOK {
		t.Errorf("re-saving an ordinary application's stored callbacks: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPatch, ordinaryPath, map[string]any{"redirect_uris": "https://wiki.example.org/cb"}, founder); response.Code != http.StatusOK {
		t.Errorf("the super administrator changing an ordinary application's callbacks: %d %s", response.Code, response.Body.String())
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

// Consent is kept per application and not per callback, so a callback added to
// an application people have already agreed to would send their codes there
// without asking again. The delegate may not add one. The account that agreed
// is still sent straight back to the callback it agreed to, and the callback
// the delegate tried to add receives nothing.
func TestASecurityAdministratorCannotSendAgreedSignInsToANewCallback(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "another-password")
	delegate(t, in, founder, operator, "security")

	const (
		callback  = "https://wiki.example.com/cb"
		attacker  = "https://attacker.example.net/cb"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	// Public, so the attacker needs nothing from the application but its
	// callback: no secret to rotate and nothing to authenticate with.
	created := in.do(http.MethodPost, "/api/admin/applications", map[string]any{
		"name": "Wiki", "redirect_uris": callback, "public": true,
	}, operator)
	if created.Code != http.StatusCreated {
		t.Fatalf("register a public application: %d %s", created.Code, created.Body.String())
	}
	application := decode[struct {
		Application struct {
			ID       string `json:"id"`
			ClientID string `json:"client_id"`
		} `json:"application"`
	}](t, created).Application
	applicationPath := "/api/admin/applications/" + application.ID

	authorize := func(redirect string) string {
		return "/oauth/authorize?" + url.Values{
			"client_id":             {application.ClientID},
			"redirect_uri":          {redirect},
			"response_type":         {"code"},
			"scope":                 {"openid profile email"},
			"state":                 {"the-state"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
		}.Encode()
	}

	asked := in.do(http.MethodGet, authorize(callback), nil, member)
	screen, err := url.Parse(asked.Header().Get("Location"))
	if err != nil || screen.Path != "/oauth/consent" || screen.Query().Get("request") == "" {
		t.Fatalf("the first sign-in did not reach the consent screen: %d %q", asked.Code, asked.Header().Get("Location"))
	}
	agreed := in.do(http.MethodPost, "/api/oauth/consent", map[string]any{
		"request": screen.Query().Get("request"), "approve": true,
	}, member)
	if agreed.Code != http.StatusOK {
		t.Fatalf("approving the application: %d %s", agreed.Code, agreed.Body.String())
	}

	widened := in.do(http.MethodPatch, applicationPath, map[string]any{
		"redirect_uris": callback + "\n" + attacker,
	}, operator)
	if widened.Code != http.StatusForbidden || errCode(t, widened) != "super_admin_required" {
		t.Fatalf("a security administrator added a callback: %d %s", widened.Code, widened.Body.String())
	}

	// Never registered, so the person is shown the problem rather than sent
	// anywhere, and no code is issued.
	stolen := in.do(http.MethodGet, authorize(attacker), nil, member)
	if location := stolen.Header().Get("Location"); location != "/oauth/consent?error=bad_redirect" {
		t.Fatalf("an authorisation to the callback the delegate tried to add was sent to %q", location)
	}

	// Nothing about the agreement has changed, so the account still goes
	// straight back to the callback it agreed to. The refusal above is what
	// keeps the new callback from inheriting that.
	again := in.do(http.MethodGet, authorize(callback), nil, member)
	if location := again.Header().Get("Location"); !strings.HasPrefix(location, callback+"?") || !strings.Contains(location, "code=") {
		t.Fatalf("the account that agreed was not sent back to its callback: %q", location)
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

// The relay that receives every verification link and code is chosen by the
// super administrator too, under the same rule as the public URL. The security
// grant keeps the sender address and the rest of the mail form, and may save
// the server as it already stands.
func TestOnlyASuperAdministratorChoosesTheSMTPServer(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	delegate(t, in, founder, operator, "security")

	saved := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "127.0.0.1", "port": 1, "username": "mailer", "from": "arc@example.com",
		"public_url": "https://arc.example.com", "password": "mail-secret",
	}, founder)
	if saved.Code != http.StatusOK {
		t.Fatalf("super administrator saving mail: %d %s", saved.Code, saved.Body.String())
	}

	for label, body := range map[string]map[string]any{
		"host": {"host": "collector.example.net", "port": 1, "username": "mailer",
			"from": "arc@example.com", "public_url": "https://arc.example.com"},
		"port": {"host": "127.0.0.1", "port": 2525, "username": "mailer",
			"from": "arc@example.com", "public_url": "https://arc.example.com"},
		"username": {"host": "127.0.0.1", "port": 1, "username": "someone-else",
			"from": "arc@example.com", "public_url": "https://arc.example.com"},
		"host with a typed password": {"host": "collector.example.net", "port": 1, "username": "mailer",
			"from": "arc@example.com", "public_url": "https://arc.example.com", "password": "typed-again"},
	} {
		response := in.do(http.MethodPut, "/api/admin/mail", body, operator)
		if response.Code != http.StatusForbidden || errCode(t, response) != "super_admin_required" {
			t.Errorf("%s: a security administrator changed the SMTP server: %d %s", label, response.Code, response.Body.String())
		}
	}
	shown := decode[struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
	}](t, in.do(http.MethodGet, "/api/admin/mail", nil, founder))
	if shown.Host != "127.0.0.1" || shown.Port != 1 || shown.Username != "mailer" {
		t.Fatalf("refused saves changed the SMTP server: %+v", shown)
	}

	if response := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "127.0.0.1", "port": 1, "username": "mailer", "from": "ops@example.com",
		"public_url": "https://arc.example.com",
	}, operator); response.Code != http.StatusOK {
		t.Fatalf("a security administrator saving the sender with the server unchanged: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPut, "/api/admin/mail", map[string]any{
		"host": "collector.example.net", "port": 1, "username": "mailer", "from": "ops@example.com",
		"public_url": "https://arc.example.com", "password": "mail-secret",
	}, founder); response.Code != http.StatusOK {
		t.Fatalf("super administrator choosing the SMTP server: %d %s", response.Code, response.Body.String())
	}
}

func providerIDOf(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[struct {
		Provider struct {
			ID string `json:"id"`
		} `json:"provider"`
	}](t, response).Provider.ID
}

// providerRow is what the providers listing shows for one provider. The key
// itself is never listed, only its hint, which is what shows whether a write
// replaced it.
type providerRow struct {
	ID         string `json:"id"`
	BaseURL    string `json:"base_url"`
	APIKeyHint string `json:"api_key_hint"`
}

func providerRowOf(t *testing.T, in *instance, as *session, providerID string) providerRow {
	t.Helper()
	listing := decode[struct {
		Providers []providerRow `json:"providers"`
	}](t, in.do(http.MethodGet, "/api/admin/providers", nil, as))
	for _, row := range listing.Providers {
		if row.ID == providerID {
			return row
		}
	}
	t.Fatalf("provider %s is not listed", providerID)
	return providerRow{}
}

// providerBaseURLs lists the address of every provider the administrator can
// see, which is how a test shows that a refused create stored nothing.
func providerBaseURLs(t *testing.T, in *instance, as *session) []string {
	t.Helper()
	listing := decode[struct {
		Providers []providerRow `json:"providers"`
	}](t, in.do(http.MethodGet, "/api/admin/providers", nil, as))
	addresses := make([]string, 0, len(listing.Providers))
	for _, row := range listing.Providers {
		addresses = append(addresses, row.BaseURL)
	}
	return addresses
}

// A provider's base URL is where every chat on it is sent, prompts and
// attachments included, so the address is the super administrator's to choose,
// as the public URL is. Moving a provider and creating one at a new address are
// the same decision. The providers grant still edits a provider's other fields,
// and duplicates one at the address it already has, which copies the key
// without showing it. A key the delegate types for a move is refused with the
// address, and nothing of the move or the create is stored.
func TestOnlyASuperAdministratorMovesAProvidersBaseURL(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	delegate(t, in, founder, operator, "providers")

	created := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Primary", "kind": "openai", "base_url": "https://api.example.com/v1", "api_key": "sk-the-real-secret-value",
	}, founder)
	if created.Code != http.StatusCreated {
		t.Fatalf("super administrator creating a provider: %d %s", created.Code, created.Body.String())
	}
	providerID := providerIDOf(t, created)
	providerPath := "/api/admin/providers/" + providerID
	before := providerRowOf(t, in, founder, providerID)

	refused := in.do(http.MethodPatch, providerPath, map[string]any{
		"name": "Primary", "kind": "openai", "base_url": "https://collector.example.net/v1", "api_key": "sk-operator-own-0002",
	}, operator)
	if refused.Code != http.StatusForbidden || errCode(t, refused) != "super_admin_required" {
		t.Fatalf("a providers-grant holder moved the base URL: %d %s", refused.Code, refused.Body.String())
	}
	if after := providerRowOf(t, in, founder, providerID); after != before {
		t.Fatalf("the refused move changed the provider: %+v, was %+v", after, before)
	}

	if response := in.do(http.MethodPatch, providerPath, map[string]any{
		"name": "Renamed", "kind": "openai", "base_url": "https://api.example.com/v1",
	}, operator); response.Code != http.StatusOK {
		t.Errorf("a providers-grant holder editing other fields, address unchanged: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Second", "kind": "openai", "base_url": "https://second.example.com/v1", "api_key": "sk-second-key-value",
	}, operator); response.Code != http.StatusForbidden || errCode(t, response) != "super_admin_required" {
		t.Errorf("a providers-grant holder creating a provider at a new address: %d %s", response.Code, response.Body.String())
	}
	if addresses := providerBaseURLs(t, in, founder); slices.Contains(addresses, "https://second.example.com/v1") {
		t.Errorf("the refused create stored a provider: %v", addresses)
	}
	if response := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Copy", "kind": "openai", "base_url": "https://collector.example.net/v1", "copy_key_from": providerID,
	}, operator); response.Code != http.StatusForbidden || errCode(t, response) != "super_admin_required" {
		t.Errorf("a providers-grant holder duplicating a provider to a new address: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Copy", "kind": "openai", "base_url": "https://api.example.com/v1", "copy_key_from": providerID,
	}, operator); response.Code != http.StatusCreated {
		t.Errorf("a providers-grant holder duplicating a provider at its own address: %d %s", response.Code, response.Body.String())
	}

	if response := in.do(http.MethodPatch, providerPath, map[string]any{
		"name": "Renamed", "kind": "openai", "base_url": "https://collector.example.net/v1", "api_key": "sk-the-new-secret-value",
	}, founder); response.Code != http.StatusOK {
		t.Fatalf("super administrator moving the base URL: %d %s", response.Code, response.Body.String())
	}
	if moved := providerRowOf(t, in, founder, providerID); moved.BaseURL != "https://collector.example.net/v1" {
		t.Errorf("base_url = %q after the super administrator's move", moved.BaseURL)
	}
}

// A provider's address decides where the key and the prompts go. A delegate
// who could choose one could point it at a server on this machine, and the
// detect call or any chat routed through the provider would carry the key
// there. Both grants that reach the detect route are held to the refusal, and
// the stand-in must never be contacted.
func TestAProvidersGrantCannotPointANewProviderAtAStandIn(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	var contacted atomic.Int32
	standIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"stand-in-model"}]}`))
	}))
	t.Cleanup(standIn.Close)
	address := standIn.URL + "/v1"

	for _, delegated := range []struct {
		username string
		grants   []string
	}{
		{"providers-only", []string{"providers"}},
		{"providers-models", []string{"providers", "models"}},
	} {
		operator := in.register(delegated.username, "a-good-password")
		delegate(t, in, founder, operator, delegated.grants...)

		response := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
			"name": "Stand-in " + delegated.username, "kind": "openai", "base_url": address, "api_key": "sk-operator-own-key",
		}, operator)
		if response.Code != http.StatusForbidden || errCode(t, response) != "super_admin_required" {
			t.Errorf("%v created a provider at a stand-in: %d %s", delegated.grants, response.Code, response.Body.String())
		}
	}

	if slices.Contains(providerBaseURLs(t, in, founder), address) {
		t.Error("a refused create stored a provider pointing at the stand-in")
	}
	if n := contacted.Load(); n != 0 {
		t.Errorf("the stand-in was contacted %d time(s)", n)
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

// A super administrator's cards can be moved or withdrawn only by a super
// administrator. A users-grant delegate still moves and withdraws a member's
// cards, which is what the grant is for, and still grants a super administrator
// more, which only adds to what they hold.
func TestOnlyASuperAdministratorMovesOrWithdrawsTheirOwnCards(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "another-password")
	delegate(t, in, founder, operator, "users")

	expires := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second).UnixMilli()
	for _, account := range []*session{founder, member} {
		if response := in.do(http.MethodPost, "/api/admin/users/"+account.userID+"/cards",
			map[string]any{"cards": 2, "expires_at": expires}, founder); response.Code != http.StatusCreated {
			t.Fatalf("grant cards: %d %s", response.Code, response.Body.String())
		}
	}
	if response := in.do(http.MethodPost, "/api/admin/users/"+founder.userID+"/cards",
		map[string]any{"cards": 1, "expires_at": expires}, operator); response.Code != http.StatusCreated {
		t.Fatalf("a users-grant delegate granting a super administrator cards: %d %s", response.Code, response.Body.String())
	}

	later := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second).UnixMilli()
	before := readerCards(t, in, founder)
	if len(before) != 3 {
		t.Fatalf("the super administrator holds %d cards, want 3", len(before))
	}
	moved := in.do(http.MethodPatch, "/api/admin/users/"+founder.userID+"/cards", map[string]any{"expires_at": later}, operator)
	if moved.Code != http.StatusForbidden || errCode(t, moved) != "admin_permission_denied" {
		t.Fatalf("a users-grant delegate moved a super administrator's cards: %d %s", moved.Code, moved.Body.String())
	}
	dropped := in.do(http.MethodDelete, "/api/admin/users/"+founder.userID+"/cards/"+before[0].ID, nil, operator)
	if dropped.Code != http.StatusForbidden || errCode(t, dropped) != "admin_permission_denied" {
		t.Fatalf("a users-grant delegate withdrew a super administrator's card: %d %s", dropped.Code, dropped.Body.String())
	}
	after := readerCards(t, in, founder)
	if len(after) != len(before) {
		t.Fatalf("the refused changes altered the super administrator's holding: %+v, was %+v", after, before)
	}
	for _, card := range after {
		if card.ExpiresAt != expires {
			t.Fatalf("a refused move changed a super administrator's card to expire at %d", card.ExpiresAt)
		}
	}

	memberCards := readerCards(t, in, member)
	if response := in.do(http.MethodPatch, "/api/admin/users/"+member.userID+"/cards",
		map[string]any{"expires_at": later}, operator); response.Code != http.StatusOK {
		t.Fatalf("a users-grant delegate moving a member's cards: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodDelete, "/api/admin/users/"+member.userID+"/cards/"+memberCards[0].ID,
		nil, operator); response.Code != http.StatusNoContent {
		t.Fatalf("a users-grant delegate withdrawing a member's card: %d %s", response.Code, response.Body.String())
	}

	if response := in.do(http.MethodPatch, "/api/admin/users/"+founder.userID+"/cards",
		map[string]any{"expires_at": later}, founder); response.Code != http.StatusOK {
		t.Fatalf("a super administrator moving their own cards: %d %s", response.Code, response.Body.String())
	}
	if response := in.do(http.MethodDelete, "/api/admin/users/"+founder.userID+"/cards/"+before[0].ID,
		nil, founder); response.Code != http.StatusNoContent {
		t.Fatalf("a super administrator withdrawing their own card: %d %s", response.Code, response.Body.String())
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
