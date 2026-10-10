package server

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The identity provider's door to the two holds, through the assembled server.
//
// A refresh token and an authorisation code are both issued before a
// requirement is switched on, and both outlive the request that asked for
// them: a refresh token renews itself for a month. So the token endpoint and
// the identity endpoint have to meet the same requirements the web and the API
// meet at their own doors. The unit tests in internal/idp build the provider
// with no requirement at all, so this is where the predicates are wired to it.

// authorisationCode takes an account through consent and returns the code the
// application would exchange, without exchanging it. Holding the exchange back
// is what lets a test switch a requirement on between the two. An account that
// has already consented goes straight to the callback, so this also serves for
// a second sign-in.
func (in *instance) authorisationCode(clientID string, as *session) (string, string) {
	in.t.Helper()
	const verifier = "idp-holds-verifier-0123456789-abcdefghijklmnopqrstuvwxyz"
	sum := sha256.Sum256([]byte(verifier))
	authorize := "/oauth/authorize?" + url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {idpCallback},
		"response_type":         {"code"},
		"scope":                 {"openid profile email"},
		"state":                 {"s"},
		"nonce":                 {"n"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}.Encode()

	asked := in.do(http.MethodGet, authorize, nil, as)
	if screen, err := url.Parse(asked.Header().Get("Location")); err == nil && screen.Query().Get("request") != "" {
		if agreed := in.do(http.MethodPost, "/api/oauth/consent", map[string]any{
			"request": screen.Query().Get("request"), "approve": true,
		}, as); agreed.Code != http.StatusOK {
			in.t.Fatalf("consent: %d %s", agreed.Code, agreed.Body.String())
		}
		asked = in.do(http.MethodGet, authorize, nil, as)
	}
	landed, err := url.Parse(asked.Header().Get("Location"))
	if err != nil || landed.Query().Get("code") == "" {
		in.t.Fatalf("no code: %d %q", asked.Code, asked.Header().Get("Location"))
	}
	return landed.Query().Get("code"), verifier
}

func (in *instance) exchangeCode(clientID, code, verifier string) *http.Response {
	in.t.Helper()
	return in.formPost("/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {idpCallback},
		"code_verifier": {verifier},
	}).Result()
}

// assertHeldAtTheProvider is the state under a requirement the account was not
// under when it signed in: its access token names nobody, its refresh token is
// refused, and its code cannot be exchanged.
func (in *instance) assertHeldAtTheProvider(clientID, refresh, access, code, verifier, requirement string) {
	in.t.Helper()
	// The identity endpoint first. A refresh rotates the row that holds the
	// access token, so asked afterwards this would be refused whatever the
	// requirement said.
	if info := in.userinfo(access); info.Code != http.StatusUnauthorized {
		in.t.Errorf("userinfo under %s with a token from before it = %d %s, want it refused",
			requirement, info.Code, info.Body.String())
	}
	refreshed := in.formPost("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refresh},
	})
	if refreshed.Code != http.StatusBadRequest || !strings.Contains(refreshed.Body.String(), "invalid_grant") {
		in.t.Errorf("refresh under %s = %d %s, want invalid_grant", requirement, refreshed.Code, refreshed.Body.String())
	}
	if exchanged := in.exchangeCode(clientID, code, verifier); exchanged.StatusCode != http.StatusBadRequest {
		in.t.Errorf("exchange under %s of a code from before it = %d, want invalid_grant", requirement, exchanged.StatusCode)
	}
}

func TestTheTwoStepHoldReachesATokenIssuedBeforeThePolicy(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	clientID := in.publicApplication(founder)

	refresh, access := in.signInThrough(clientID, member)
	code, verifier := in.authorisationCode(clientID, member)

	// The founder enrols first, or the policy holds them out of the settings
	// screen that would switch it off again.
	in.enrol(founder)
	policy := map[string]string{"security.two_factor_policy": "everyone"}
	if response := in.do(http.MethodPut, "/api/admin/settings", policy, founder); response.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", response.Code, response.Body.String())
	}
	if held := in.do(http.MethodGet, "/api/conversations", nil, member); held.Code != http.StatusForbidden {
		t.Fatalf("the web is not holding the member: %d %s", held.Code, held.Body.String())
	}

	in.assertHeldAtTheProvider(clientID, refresh, access, code, verifier, "the two-step policy")

	// A hold lifts with the requirement: once the member has enrolled, a sign-in
	// that starts again is given its code.
	in.enrol(member)
	fresh, freshVerifier := in.authorisationCode(clientID, member)
	if exchanged := in.exchangeCode(clientID, fresh, freshVerifier); exchanged.StatusCode != http.StatusOK {
		t.Fatalf("a sign-in after enrolling = %d, want the code exchanged", exchanged.StatusCode)
	}
}

func TestTheOIDCBindingHoldReachesATokenIssuedBeforeThePolicy(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	clientID := in.publicApplication(founder)

	refresh, access := in.signInThrough(clientID, member)
	code, verifier := in.authorisationCode(clientID, member)

	stub := stubOIDCProvider(t)
	configure := map[string]string{
		"oauth.oidc_enabled":       "true",
		"oauth.oidc_client_id":     "a-client-id",
		"oauth.oidc_client_secret": "a-client-secret",
		"oauth.oidc_auth_url":      stub.URL + "/authorize",
		"oauth.oidc_token_url":     stub.URL + "/token",
		"oauth.oidc_userinfo_url":  stub.URL + "/userinfo",
	}
	if response := in.do(http.MethodPut, "/api/admin/settings", configure, founder); response.Code != http.StatusOK {
		t.Fatalf("configure: %d %s", response.Code, response.Body.String())
	}
	policy := map[string]string{"oauth.oidc_require_for_all": "true"}
	if response := in.do(http.MethodPut, "/api/admin/settings", policy, founder); response.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", response.Code, response.Body.String())
	}
	if held := in.do(http.MethodGet, "/api/conversations", nil, member); held.Code != http.StatusForbidden {
		t.Fatalf("the web is not holding the member: %d %s", held.Code, held.Body.String())
	}

	in.assertHeldAtTheProvider(clientID, refresh, access, code, verifier, "the OIDC binding policy")
}

// A hold applies to the account it names and to nothing else. An account that is
// not held keeps consent, the code exchange, renewal, the identity endpoint and
// revocation exactly as they were, so the provider's new checks cannot be the
// reason an ordinary sign-in stops working.
func TestAnUnheldAccountKeepsItsProviderAccessUnderTheHolds(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	clientID := in.publicApplication(founder)

	// Enrolled before the policy is switched on, so the policy has nothing to
	// hold this account for.
	in.enrol(founder)
	policy := map[string]string{"security.two_factor_policy": "everyone"}
	if response := in.do(http.MethodPut, "/api/admin/settings", policy, founder); response.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", response.Code, response.Body.String())
	}
	if held := in.do(http.MethodGet, "/api/conversations", nil, founder); held.Code != http.StatusOK {
		t.Fatalf("an enrolled account is held: %d %s", held.Code, held.Body.String())
	}

	refresh, access := in.signInThrough(clientID, founder)
	if info := in.userinfo(access); info.Code != http.StatusOK {
		t.Fatalf("userinfo for an unheld account = %d %s", info.Code, info.Body.String())
	}
	renewed := in.formPost("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refresh},
	})
	if renewed.Code != http.StatusOK {
		t.Fatalf("refresh for an unheld account = %d %s", renewed.Code, renewed.Body.String())
	}
	renewedAccess := decode[struct {
		AccessToken string `json:"access_token"`
	}](t, renewed).AccessToken
	if info := in.userinfo(renewedAccess); info.Code != http.StatusOK {
		t.Fatalf("userinfo with the renewed token = %d %s", info.Code, info.Body.String())
	}

	revoked := in.formPost("/oauth/revoke", url.Values{
		"token": {renewedAccess}, "client_id": {clientID},
	})
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke = %d %s", revoked.Code, revoked.Body.String())
	}
	if info := in.userinfo(renewedAccess); info.Code != http.StatusUnauthorized {
		t.Fatalf("userinfo after revoking = %d, want it refused", info.Code)
	}
}

// A held account gets its provider access back once it connects the identity
// the policy asks for, as a two-step account does once it enrols. The hold is
// a state of the account, so lifting it is all a new sign-in needs.
func TestTheOIDCBindingHoldLiftsOnceTheAccountConnects(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	clientID := in.publicApplication(founder)

	stub := stubOIDCProvider(t)
	configure := map[string]string{
		"oauth.oidc_enabled":       "true",
		"oauth.oidc_client_id":     "a-client-id",
		"oauth.oidc_client_secret": "a-client-secret",
		"oauth.oidc_auth_url":      stub.URL + "/authorize",
		"oauth.oidc_token_url":     stub.URL + "/token",
		"oauth.oidc_userinfo_url":  stub.URL + "/userinfo",
	}
	if response := in.do(http.MethodPut, "/api/admin/settings", configure, founder); response.Code != http.StatusOK {
		t.Fatalf("configure: %d %s", response.Code, response.Body.String())
	}
	policy := map[string]string{"oauth.oidc_require_for_all": "true"}
	if response := in.do(http.MethodPut, "/api/admin/settings", policy, founder); response.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", response.Code, response.Body.String())
	}
	if held := in.do(http.MethodGet, "/api/conversations", nil, member); held.Code != http.StatusForbidden {
		t.Fatalf("the member is not held: %d %s", held.Code, held.Body.String())
	}

	stateCookie, state := in.startConnect("oidc", member, "another-password", "")
	callback := in.doWithExtraCookies(http.MethodGet,
		"/api/auth/oauth/callback/oidc?code=c&state="+state, member, stateCookie)
	if callback.Code != http.StatusFound || !strings.HasPrefix(callback.Header().Get("Location"), "/settings?oauth=connected") {
		t.Fatalf("finish the link: %d %s", callback.Code, callback.Header().Get("Location"))
	}
	if held := in.do(http.MethodGet, "/api/conversations", nil, member); held.Code != http.StatusOK {
		t.Fatalf("still held after connecting: %d %s", held.Code, held.Body.String())
	}

	code, verifier := in.authorisationCode(clientID, member)
	exchanged := in.formPost("/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {idpCallback},
		"code_verifier": {verifier},
	})
	if exchanged.Code != http.StatusOK {
		t.Fatalf("a sign-in after connecting = %d %s, want the code exchanged", exchanged.Code, exchanged.Body.String())
	}
	access := decode[struct {
		AccessToken string `json:"access_token"`
	}](t, exchanged).AccessToken
	if info := in.userinfo(access); info.Code != http.StatusOK {
		t.Fatalf("userinfo after connecting = %d %s", info.Code, info.Body.String())
	}
}
