package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// An application that signed a person in keeps two things: a refresh token that
// renews for a month, and the access token it renews into. A password is what
// the person changes when they stop trusting whoever had their session, so it
// has to end both. The consent is not touched; the person still agreed.

const idpCallback = "https://wiki.example.com/cb"

// publicApplication registers a public application the way the administrative
// screen does, and returns its client id.
func (in *instance) publicApplication(admin *session) string {
	in.t.Helper()
	created := in.do(http.MethodPost, "/api/admin/applications", map[string]any{
		"name": "Wiki", "redirect_uris": idpCallback, "public": true,
	}, admin)
	if created.Code != http.StatusCreated {
		in.t.Fatalf("register a public application: %d %s", created.Code, created.Body.String())
	}
	return decode[struct {
		Application struct {
			ClientID string `json:"client_id"`
		} `json:"application"`
	}](in.t, created).Application.ClientID
}

// formPost sends a form to one of the protocol endpoints, which take no session.
func (in *instance) formPost(path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

// signInThrough takes an account through consent and the code exchange, and
// returns the refresh token and access token the application was given.
func (in *instance) signInThrough(clientID string, as *session) (refresh, access string) {
	in.t.Helper()
	const verifier = "idp-password-verifier-0123456789-abcdefghijklmnopqrstuvwxyz"
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {idpCallback},
		"response_type":         {"code"},
		"scope":                 {"openid profile email"},
		"state":                 {"s"},
		"nonce":                 {"n"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}.Encode()
	authorize := "/oauth/authorize?" + query

	asked := in.do(http.MethodGet, authorize, nil, as)
	screen, err := url.Parse(asked.Header().Get("Location"))
	if err != nil || screen.Query().Get("request") == "" {
		in.t.Fatalf("the sign-in did not reach consent: %d %q", asked.Code, asked.Header().Get("Location"))
	}
	if agreed := in.do(http.MethodPost, "/api/oauth/consent", map[string]any{
		"request": screen.Query().Get("request"), "approve": true,
	}, as); agreed.Code != http.StatusOK {
		in.t.Fatalf("consent: %d %s", agreed.Code, agreed.Body.String())
	}

	granted := in.do(http.MethodGet, authorize, nil, as)
	landed, err := url.Parse(granted.Header().Get("Location"))
	if err != nil || landed.Query().Get("code") == "" {
		in.t.Fatalf("no code after consent: %d %q", granted.Code, granted.Header().Get("Location"))
	}
	exchanged := in.formPost("/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {landed.Query().Get("code")},
		"redirect_uri":  {idpCallback},
		"code_verifier": {verifier},
	})
	if exchanged.Code != http.StatusOK {
		in.t.Fatalf("exchange: %d %s", exchanged.Code, exchanged.Body.String())
	}
	tokens := decode[struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}](in.t, exchanged)
	return tokens.RefreshToken, tokens.AccessToken
}

func (in *instance) userinfo(access string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+access)
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

// assertApplicationShutOut is the state after the password changed: the refresh
// token is refused as an invalid grant, the access token no longer names anyone,
// and the consent the person gave is still on record.
func (in *instance) assertApplicationShutOut(clientID, refresh, access, victimID, who string) {
	in.t.Helper()
	refreshed := in.formPost("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refresh},
	})
	if refreshed.Code != http.StatusBadRequest || !strings.Contains(refreshed.Body.String(), "invalid_grant") {
		in.t.Errorf("refresh after %s = %d %s, want invalid_grant", who, refreshed.Code, refreshed.Body.String())
	}
	if info := in.userinfo(access); info.Code != http.StatusUnauthorized {
		in.t.Errorf("userinfo with the access token from before %s = %d, want it refused", who, info.Code)
	}
	grants, err := in.server.idp.GrantsFor(context.Background(), victimID)
	if err != nil || len(grants) != 1 {
		in.t.Errorf("authorisations after %s = %+v (%v), want the consent kept", who, grants, err)
	}
}

func TestAPasswordChangeEndsWhatAnApplicationHolds(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("member", "another-password")
	clientID := in.publicApplication(founder)
	refresh, access := in.signInThrough(clientID, victim)

	if info := in.userinfo(access); info.Code != http.StatusOK {
		t.Fatalf("userinfo before the change = %d %s", info.Code, info.Body.String())
	}
	changed := in.do(http.MethodPost, "/api/profile/password", map[string]string{
		"current_password": "another-password", "new_password": "a-brand-new-password",
	}, victim)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("password change: %d %s", changed.Code, changed.Body.String())
	}

	in.assertApplicationShutOut(clientID, refresh, access, victim.userID, "the password change")
}

func TestAnAdministratorsPasswordResetEndsWhatAnApplicationHolds(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("member", "another-password")
	clientID := in.publicApplication(founder)
	refresh, access := in.signInThrough(clientID, victim)

	reset := in.do(http.MethodPost, "/api/admin/users/"+victim.userID+"/password", map[string]string{
		"new_password": "a-brand-new-password",
	}, founder)
	if reset.Code != http.StatusNoContent {
		t.Fatalf("administrator reset: %d %s", reset.Code, reset.Body.String())
	}

	in.assertApplicationShutOut(clientID, refresh, access, victim.userID, "the administrator's reset")
}
