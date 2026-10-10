// Package oauth is signing in with an account somebody already holds
// somewhere else: GitHub, or Google.
//
// This instance is the client, never the provider. Nothing here issues a
// token for anybody else to check — it asks GitHub or Google who is at the
// browser, and turns that answer into a session of this instance's own.
//
// Two providers rather than a plugin system. A generic OAuth client would be
// configuration an operator has to get exactly right on a screen that cannot
// tell them they got it wrong, and the two below are the two anybody asks
// for. A third is a constant and a function, not a redesign.
//
// The endpoints are constants for the same reason Turnstile's verification
// URL is: an operator who could point this at another host could point it at
// one that says whatever it likes about who is signing in.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// How long to wait for a provider. Longer than either should ever take, and
// short enough that an outage at GitHub is a failed sign-in rather than a
// request that never ends.
const timeout = 15 * time.Second

var (
	// The provider refused, or the browser came back without a code. Usually
	// somebody pressing "cancel" on the consent screen.
	ErrDenied = errors.New("oauth: the provider did not authorise this sign-in")
	// The provider could not be reached, or answered something unreadable.
	// Not the visitor's doing, and told apart from the rest because it is the
	// operator who has to fix it.
	ErrUnavailable = errors.New("oauth: the provider is unavailable")
	// The instance has no client id and secret for this provider, so there is
	// nothing to start.
	ErrNotConfigured = errors.New("oauth: this provider is not configured here")
	// The state cookie was missing, stale, or did not match the one that came
	// back. Every one of those has the same answer: start again.
	ErrState = errors.New("oauth: this sign-in could not be verified")
)

// Identity is what a provider says about the person at the browser.
//
// Email is deliberately empty unless the provider states the address is
// verified. An unverified address is a claim, not an identity, and this
// server adopts one only where somebody else has already proved it —
// otherwise anybody could put a stranger's address on a new provider account
// and walk into the account here that belongs to it.
type Identity struct {
	Provider string
	Subject  string
	Login    string
	Name     string
	Email    string
}

// Provider is one place people already have an account.
type Provider struct {
	ID   string
	Name string
	// Where the browser is sent, and where the code is exchanged.
	AuthURL  string
	TokenURL string
	Scopes   []string
	// Whether the authorisation request carries a PKCE challenge. Google
	// documents support for it; GitHub's OAuth apps do not, and a verifier
	// sent for a challenge the provider never saw is a request it may simply
	// refuse. The state cookie is what binds the exchange to this browser on
	// both, so this is a second lock rather than the only one.
	PKCE bool
	// Reads the provider's own account endpoints with the token just issued.
	identify func(context.Context, *http.Client, string) (Identity, error)
	// For OIDC or providers that require full token exchange response and credentials.
	identifyTokens func(context.Context, *http.Client, Credentials, tokenResponse) (Identity, error)
}

var providers = []*Provider{
	{
		ID:       "github",
		Name:     "GitHub",
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token",
		// read:user rather than the broader user: this needs a name and an
		// address, not the ability to change a profile. user:email is its own
		// scope because the address is absent from the account endpoint's
		// answer unless its owner made it public.
		Scopes:   []string{"read:user", "user:email"},
		identify: identifyGitHub,
	},
	{
		ID:       "google",
		Name:     "Google",
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
		Scopes:   []string{"openid", "email", "profile"},
		PKCE:     true,
		identify: identifyGoogle,
	},
	{
		ID:             "oidc",
		Name:           "OpenID Connect",
		Scopes:         []string{"openid", "email", "profile"},
		PKCE:           true,
		identifyTokens: identifyOIDC,
	},
}

// Providers is every provider this build knows how to talk to, in the order
// the sign-in card draws them.
func Providers() []*Provider { return providers }

// ByID resolves a path segment. Nil for anything else, which is what keeps an
// invented provider name from reaching a settings lookup.
func ByID(id string) *Provider {
	for _, provider := range providers {
		if provider.ID == id {
			return provider
		}
	}
	return nil
}

// Credentials are what an operator pastes in from the provider's console.
type Credentials struct {
	ClientID     string
	ClientSecret string
	Issuer       string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	Scopes       []string
	DisplayName  string
	TrustEmail   bool
}

func (c Credentials) configuredFor(providerID string) bool {
	if strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" {
		return false
	}
	if providerID == "oidc" {
		return strings.TrimSpace(c.Issuer) != "" || (strings.TrimSpace(c.AuthURL) != "" && strings.TrimSpace(c.TokenURL) != "")
	}
	return true
}

func (c Credentials) configured() bool {
	return c.configuredFor("")
}

// authorise is the URL the browser is sent to.
func (p *Provider) authorise(creds Credentials, redirect, state, challenge string) string {
	authURL := p.AuthURL
	if creds.AuthURL != "" {
		authURL = creds.AuthURL
	}
	if authURL == "" {
		return ""
	}
	scopes := p.Scopes
	if len(creds.Scopes) > 0 {
		scopes = creds.Scopes
	}
	query := url.Values{
		"client_id":     {creds.ClientID},
		"redirect_uri":  {redirect},
		"response_type": {"code"},
		"scope":         {strings.Join(scopes, " ")},
		"state":         {state},
	}
	if p.PKCE && challenge != "" {
		query.Set("code_challenge", challenge)
		query.Set("code_challenge_method", "S256")
	}
	return authURL + "?" + query.Encode()
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// exchange trades the code for an access token.
//
// The redirect URI is sent again because both providers check it against the
// one the code was issued for. That check is what makes an intercepted code
// useless anywhere but here.
func (p *Provider) exchange(
	ctx context.Context, client *http.Client, creds Credentials, code, redirect, verifier string,
) (tokenResponse, error) {
	tokenURL := p.TokenURL
	if creds.TokenURL != "" {
		tokenURL = creds.TokenURL
	}
	form := url.Values{
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirect},
		"grant_type":    {"authorization_code"},
	}
	if p.PKCE && verifier != "" {
		form.Set("code_verifier", verifier)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// GitHub answers in form encoding unless it is asked for JSON. Google
	// ignores the header and answers JSON either way.
	request.Header.Set("Accept", "application/json")

	var body tokenResponse
	if err := fetchJSON(ctx, client, request, &body); err != nil {
		return tokenResponse{}, err
	}
	if body.Error != "" {
		// The provider naming its own refusal — an expired code, a secret
		// that does not match. Nothing the visitor can do except start again,
		// so it reads as a denial rather than as an outage.
		return tokenResponse{}, fmt.Errorf("%w: %s", ErrDenied, body.Error)
	}
	if body.AccessToken == "" && body.IDToken == "" {
		return tokenResponse{}, fmt.Errorf("%w: no access token in the reply", ErrUnavailable)
	}
	return body, nil
}

// Authenticate runs the half of the flow that happens after the browser comes
// back: exchange the code, then ask the provider whose it is.
func (p *Provider) Authenticate(
	ctx context.Context, client *http.Client, creds Credentials, code, redirect, verifier string,
) (Identity, error) {
	if !creds.configuredFor(p.ID) {
		return Identity{}, ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tokens, err := p.exchange(ctx, client, creds, code, redirect, verifier)
	if err != nil {
		return Identity{}, err
	}
	var identity Identity
	if p.identifyTokens != nil {
		identity, err = p.identifyTokens(ctx, client, creds, tokens)
	} else if p.identify != nil {
		identity, err = p.identify(ctx, client, tokens.AccessToken)
	} else {
		return Identity{}, fmt.Errorf("%w: provider %s has no identify method", ErrUnavailable, p.ID)
	}
	if err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(identity.Subject) == "" {
		return Identity{}, fmt.Errorf("%w: the provider named no account", ErrUnavailable)
	}
	identity.Provider = p.ID
	return identity, nil
}

// --- the provider account endpoints -------------------------------------------

func identifyGitHub(ctx context.Context, client *http.Client, token string) (Identity, error) {
	var account struct {
		// A number in GitHub's JSON, and an identifier here: decoded as a
		// string rather than through float64, which rounds silently past
		// 2^53 and would eventually hand one account's rows to another.
		ID    json.Number `json:"id"`
		Login string      `json:"login"`
		Name  string      `json:"name"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user", token, &account); err != nil {
		return Identity{}, err
	}

	identity := Identity{
		Subject: account.ID.String(),
		Login:   account.Login,
		Name:    account.Name,
	}

	// The address comes from the addresses endpoint rather than from the
	// account above, because only this one says whether it was ever
	// confirmed. A primary address GitHub has not verified is left behind:
	// see Identity.
	var addresses []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user/emails", token, &addresses); err != nil {
		// Not fatal. The person can still sign in and add an address here,
		// and refusing the whole sign-in over a scope they declined is a
		// worse answer than an account with no address on it.
		return identity, nil
	}
	for _, address := range addresses {
		if address.Primary && address.Verified {
			identity.Email = address.Email
			break
		}
	}
	return identity, nil
}

func identifyGoogle(ctx context.Context, client *http.Client, token string) (Identity, error) {
	var account struct {
		Subject  string   `json:"sub"`
		Email    string   `json:"email"`
		Verified flexBool `json:"email_verified"`
		Name     string   `json:"name"`
	}
	if err := getJSON(ctx, client,
		"https://openidconnect.googleapis.com/v1/userinfo", token, &account); err != nil {
		return Identity{}, err
	}
	identity := Identity{Subject: account.Subject, Name: account.Name}
	if bool(account.Verified) {
		identity.Email = account.Email
		// Google offers no second name to suggest a username from, and the
		// local part of the address is a better one than a display name with
		// a space in it.
		if at := strings.Index(account.Email, "@"); at > 0 {
			identity.Login = account.Email[:at]
		}
	}
	return identity, nil
}

// flexBool reads either a JSON boolean or the string spelling of one.
//
// Google's OpenID userinfo answers with a boolean and its older endpoints
// answered with "true". Getting this wrong fails quietly in the worse
// direction — a verified address read as unverified is an account that cannot
// be linked and a sign-in that opens a second one — so both are accepted.
type flexBool bool

func (b *flexBool) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	switch text {
	case "true":
		*b = true
	case "false", "null", "":
		*b = false
	default:
		return fmt.Errorf("oauth: %q is not a boolean", text)
	}
	return nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint, token string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	// GitHub refuses a request that does not name its client.
	request.Header.Set("User-Agent", "obsidian-arc")
	return fetchJSON(ctx, client, request, into)
}

func fetchJSON(ctx context.Context, client *http.Client, request *http.Request, into any) error {
	// Every request here carries the client secret or a bearer token. Following
	// a redirect would send the same form body or token on to the Location,
	// which may be plain http even though this request was https, or another
	// host altogether. So a redirect is the provider's answer rather than a hop
	// to take: it fails the status check below, and the sign-in is unavailable.
	// Every hop is refused, not only the plaintext ones, because an https host
	// the reply names is no more entitled to the secret than an http one. A
	// copy of the client, so the caller's own is not changed, and the default
	// client is covered by the same rule.
	var copied http.Client
	if client != nil {
		copied = *client
	}
	copied.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	reply, err := copied.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = reply.Body.Close() }()

	// Bounded, because this is somebody else's server and the answer is a
	// handful of fields. A provider having a bad day must not be able to read
	// this process out of memory.
	body, err := io.ReadAll(io.LimitReader(reply.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	switch {
	case reply.StatusCode == http.StatusUnauthorized, reply.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: status %d", ErrDenied, reply.StatusCode)
	case reply.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: status %d", ErrUnavailable, reply.StatusCode)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nil
}
