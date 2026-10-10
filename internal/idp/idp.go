// Package idp is the other half of signing in: this instance as the place
// somebody else's site sends people to.
//
// An application registered here puts a "Sign in with <this instance>" button
// on its own pages. A visitor who presses it arrives at this server, sees who
// is asking and what they would learn, says yes, and goes back with a code
// their application trades for an identity token. The account never leaves,
// the password never leaves, and the application learns exactly the claims on
// the scopes it was granted.
//
// It is OpenID Connect rather than a scheme of this project's own, because
// the point of the feature is that other software already knows how to do
// this. The discovery document, the key set, the token and the identity
// endpoint are all where a client library expects to find them; nothing here
// asks the other side to be written specially.
//
// Not to be confused with internal/oauth, which points the other way: there
// this instance is the client, asking GitHub or Google who is at the browser.
// Here it is the one being asked.
//
// Nothing issued here can spend anything. A token from this package answers
// the identity endpoint and nothing else — provider credit goes through
// internal/apikey, which is a different credential with a different ceiling
// and a different way of being revoked.
package idp

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("idp: no such application")
	// The client_id is unknown, disabled, or the secret does not match. One
	// error for all three: an application that cannot authenticate has no
	// business learning which of the three it was.
	ErrClientAuth = errors.New("idp: the application could not be authenticated")
	// The callback is not one this application registered. Never followed,
	// and never reported back to the browser by redirecting to it — an
	// unregistered callback is exactly where an attacker wants the code sent.
	ErrRedirectMismatch = errors.New("idp: that callback is not registered for this application")
	ErrInvalidRequest   = errors.New("idp: the authorisation request is not valid")
	ErrUnknownScope     = errors.New("idp: that scope is not one this application may ask for")
	// The code is unknown, expired, already spent, or belongs to another
	// application. Same answer to all of them, for the same reason.
	ErrBadCode  = errors.New("idp: that authorisation code cannot be used")
	ErrBadToken = errors.New("idp: that token cannot be used")
	ErrPKCE     = errors.New("idp: the PKCE verifier does not match the challenge")
	// A public application — one with no secret — that did not bring a PKCE
	// challenge. Without one there is nothing tying the code to whoever
	// started the sign-in.
	ErrPKCERequired = errors.New("idp: this application must use PKCE")

	ErrInvalidName      = errors.New("idp: name must be 1-60 characters")
	ErrNoRedirectURI    = errors.New("idp: an application needs at least one callback URL")
	ErrBadRedirectURI   = errors.New("idp: a callback must be an absolute http(s) URL with no fragment")
	ErrInsecureRedirect = errors.New("idp: a callback must use https, except on localhost")
)

// How long each thing lives.
//
// The code is short because it crosses the browser and is spent immediately
// by a server that is already waiting for it; anything longer is a window in
// which a code sitting in a proxy log is still worth something. The access
// token is an hour because that is what everything else assumes, and the
// refresh token is the only long-lived thing here — revocable, rotated on
// every use, and the one an operator removes when somebody leaves.
const (
	CodeTTL    = 2 * time.Minute
	TokenTTL   = time.Hour
	RefreshTTL = 30 * 24 * time.Hour
	// A ceiling on how many applications may exist. Not a security control —
	// only an administrator can register one — but an unbounded list is not a
	// list, and the consent screen has to be able to name what is asking.
	MaxApps = 100
	// Bounds on what an operator types.
	MaxNameChars        = 60
	MaxDescriptionChars = 300
	MaxRedirectURIs     = 10
	MaxRedirectURIChars = 500
)

// The claims an application may ask for. Deliberately short: this exists to
// say who somebody is, and every scope is a thing the person consenting has
// to understand from one line on a screen.
const (
	// Required. Its presence is what makes the request an identity request
	// rather than a bare OAuth one, and it is what asks for the subject.
	ScopeOpenID = "openid"
	// The display name, the username and the avatar.
	ScopeProfile = "profile"
	// The address and whether this server has seen it confirmed.
	ScopeEmail = "email"
	// The account's group here, so an application can map it to a role of its
	// own rather than treating every visitor the same.
	ScopeGroups = "groups"
)

var knownScopes = []string{ScopeOpenID, ScopeProfile, ScopeEmail, ScopeGroups}

// DefaultScopes is what a newly registered application may ask for.
var DefaultScopes = []string{ScopeOpenID, ScopeProfile, ScopeEmail}

func KnownScopes() []string { return append([]string(nil), knownScopes...) }

func validScope(scope string) bool {
	for _, known := range knownScopes {
		if scope == known {
			return true
		}
	}
	return false
}

// ParseScopes splits a space-separated list, drops what is not a scope here,
// and returns what is left with no repeats.
//
// Unknown scopes are dropped rather than refused, because the specification
// says a server may narrow what was asked for and because a client library
// that always sends one extra word should not be unusable. What the
// application is actually granted is what comes back, and it is what the
// consent screen lists.
//
// The order is knownScopes' own rather than the order they arrived in or the
// alphabet: it has to be stable, because the result is compared and stored —
// and this is the order the consent screen reads in, which is who you are
// first and the details after. An alphabetical list opens on "your email
// address", which is the wrong thing to lead with.
func ParseScopes(raw string) []string {
	asked := map[string]bool{}
	for _, field := range strings.Fields(raw) {
		if validScope(field) {
			asked[field] = true
		}
	}
	out := []string{}
	for _, scope := range knownScopes {
		if asked[scope] {
			out = append(out, scope)
		}
	}
	return out
}

// Covers reports whether a set of granted scopes includes every one asked
// for. It is what decides whether a returning visitor sees the consent screen
// again: a wider request is a new question, a narrower one is not.
func Covers(granted, wanted []string) bool {
	have := map[string]bool{}
	for _, scope := range granted {
		have[scope] = true
	}
	for _, scope := range wanted {
		if !have[scope] {
			return false
		}
	}
	return true
}

// within keeps the granted scopes that the application may still ask for. An
// operator who takes a scope away has asked for it to stop arriving, and a token
// issued before that keeps its own list, so the list that goes out is computed
// at the moment it is used. The stored list is not rewritten: it is what the
// person consented to, so restoring the scope gives it back to the tokens that
// held it.
func within(granted, allowed []string) []string {
	out := []string{}
	for _, scope := range granted {
		if Covers(allowed, []string{scope}) {
			out = append(out, scope)
		}
	}
	return out
}

// App is a registered application. The secret is absent by design: the row
// holds a digest, and there is no field here for a value that exists once.
type App struct {
	ID          string `json:"id"`
	ClientID    string `json:"client_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Exact callbacks. Matching is string equality, so a trailing slash is a
	// different callback — which is what an operator's first failed attempt
	// usually turns out to be, and why the screen shows what is registered.
	RedirectURIs []string `json:"redirect_uris"`
	Scopes       []string `json:"scopes"`
	Trusted      bool     `json:"trusted"`
	Disabled     bool     `json:"disabled"`
	// Whether the application holds a secret. False is a public client, which
	// must bring a PKCE challenge instead.
	Confidential bool   `json:"confidential"`
	CreatedBy    string `json:"created_by"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Allows reports whether a callback is one this application registered.
func (a App) Allows(redirectURI string) bool {
	for _, candidate := range a.RedirectURIs {
		if candidate == redirectURI {
			return true
		}
	}
	return false
}

// Grant is one account's standing agreement with one application.
type Grant struct {
	AppID      string   `json:"app_id"`
	ClientID   string   `json:"client_id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	CreatedAt  int64    `json:"created_at"`
	LastUsedAt int64    `json:"last_used_at"`
}

// --- what an operator types ---------------------------------------------------

func checkName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len([]rune(name)) > MaxNameChars {
		return "", ErrInvalidName
	}
	return name, nil
}

// ParseRedirectURIs reads the textarea an operator fills in: one callback per
// line, or separated by commas, because both are what people type.
func ParseRedirectURIs(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		candidate := strings.TrimSpace(field)
		if candidate == "" || seen[candidate] {
			continue
		}
		if err := checkRedirectURI(candidate); err != nil {
			return nil, err
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	if len(out) == 0 {
		return nil, ErrNoRedirectURI
	}
	if len(out) > MaxRedirectURIs {
		return nil, ErrNoRedirectURI
	}
	return out, nil
}

func checkRedirectURI(candidate string) error {
	if len(candidate) > MaxRedirectURIChars {
		return ErrBadRedirectURI
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" || parsed.Fragment != "" || strings.Contains(candidate, "#") {
		return ErrBadRedirectURI
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		// Plain HTTP sends the code back in the clear. Allowed only where the
		// wire is inside one machine, which is where somebody is developing
		// against this and has no certificate yet.
		host := parsed.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return ErrInsecureRedirect
	default:
		return ErrBadRedirectURI
	}
}
