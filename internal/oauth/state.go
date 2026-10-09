package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Carrying a sign-in across the trip to the provider and back.
//
// Nothing about a half-finished sign-in is stored. What has to survive the
// round trip — which provider, which browser, the PKCE verifier, and whether
// this is a new session or a link onto an account already signed in — goes
// into a cookie, signed with the instance secret so that it can be believed
// when it comes back.
//
// The signature is the point. The cookie is HttpOnly, but a cookie can also
// be written by anything that has managed to get onto a neighbouring
// hostname, and a forged one is a real attack: plant your own half-finished
// sign-in in somebody else's browser and they come back signed in as you,
// typing into an account you can read. A value this process did not sign is
// not a state, it is noise.

const (
	// Long enough to read a consent screen and sign in to the provider first,
	// short enough that a cookie left on a shared machine is not a way in.
	stateTTL = 10 * time.Minute
	// Path-scoped so it rides only on the two requests that need it, and
	// named for what it is rather than after the session cookie.
	stateCookie = "oa_oauth"
)

// state is what the callback has to know and cannot be told by the caller.
type state struct {
	Provider string `json:"p"`
	// The opaque value handed to the provider and compared with what comes
	// back. Signing the cookie proves this browser started a sign-in; this
	// proves the answer belongs to that one rather than to another tab.
	Nonce string `json:"n"`
	// PKCE, where the provider supports it.
	Verifier string `json:"v,omitempty"`
	// Set when an account that is already signed in is adding a connection,
	// so the callback links rather than opens a session.
	UserID string `json:"u,omitempty"`
	// Where to send the browser afterwards. A path of this site, never a URL:
	// the whole value is put in a Location header, and one that could carry a
	// host would make this endpoint an open redirect with a provider's name
	// on it.
	Next   string `json:"r,omitempty"`
	Expiry int64  `json:"e"`
}

// pending is a sign-in that stopped to ask something.
//
// The provider has already said who this is; what is missing is a QQ number or
// an address that only the person can give. Signed and put in a cookie for the
// same reason the state above is: it crosses a page, and a page is somewhere a
// value can be edited — and the value here is which GitHub account is about to
// become an account on this server.
//
// Nothing is written while this is outstanding. An abandoned form leaves a
// cookie that expires, and no row anywhere.
type pending struct {
	Provider string `json:"p"`
	Subject  string `json:"s"`
	Login    string `json:"l"`
	Name     string `json:"m"`
	// Only ever the address the provider proved. One somebody types into the
	// form it leads to is never kept here: it would then be signed by this
	// server, which is the one thing that must not happen to an unproven
	// address.
	Email  string `json:"e"`
	Next   string `json:"r,omitempty"`
	Expiry int64  `json:"x"`
}

// How long somebody has to fill the form in. Long enough to go and look up a
// QQ number, short enough that a cookie left on a shared machine is not a way
// to open an account as somebody else.
const pendingTTL = 20 * time.Minute

// Its own cookie, and its own signature derivation, so that a state cannot be
// presented as a pending sign-up or the other way round.
const pendingCookie = "oa_oauth_signup"

// stamp signs and reads the cookie.
type stamp struct{ key []byte }

func newStamp(secret []byte) *stamp {
	// Derived rather than used raw, so this cannot be confused with anything
	// else the same instance secret signs.
	sum := sha256.Sum256(append([]byte("obsidian-arc/oauth-state\x00"), secret...))
	return &stamp{key: sum[:]}
}

// newPendingStamp derives the other key. Same secret, different purpose, so a
// state cannot be presented as a half-finished sign-up or the other way round.
func newPendingStamp(secret []byte) *stamp {
	sum := sha256.Sum256(append([]byte("obsidian-arc/oauth-signup\x00"), secret...))
	return &stamp{key: sum[:]}
}

func (s *stamp) issuePending(value pending) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	return encoded + "." + s.tag(encoded), nil
}

func (s *stamp) readPending(cookie string) (pending, error) {
	encoded, tag, ok := strings.Cut(strings.TrimSpace(cookie), ".")
	if !ok || !hmac.Equal([]byte(tag), []byte(s.tag(encoded))) {
		return pending{}, ErrState
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return pending{}, ErrState
	}
	var value pending
	if err := json.Unmarshal(body, &value); err != nil {
		return pending{}, ErrState
	}
	if time.Now().UnixMilli() > value.Expiry {
		return pending{}, ErrState
	}
	return value, nil
}

func (s *stamp) issue(value state) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	return encoded + "." + s.tag(encoded), nil
}

func (s *stamp) read(cookie string) (state, error) {
	encoded, tag, ok := strings.Cut(strings.TrimSpace(cookie), ".")
	if !ok {
		return state{}, ErrState
	}
	if !hmac.Equal([]byte(tag), []byte(s.tag(encoded))) {
		return state{}, ErrState
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return state{}, ErrState
	}
	var value state
	if err := json.Unmarshal(body, &value); err != nil {
		return state{}, ErrState
	}
	if time.Now().UnixMilli() > value.Expiry {
		return state{}, ErrState
	}
	return value, nil
}

func (s *stamp) tag(body string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// --- the cookie ---------------------------------------------------------------

func (h *Handlers) setState(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    value,
		Path:     statePath,
		HttpOnly: true,
		Secure:   h.SecureCookie,
		// Lax rather than Strict: the provider sends the browser back with a
		// top-level GET, and Strict would withhold the cookie on exactly that
		// navigation — the one request it exists for.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(stateTTL.Seconds()),
	})
}

func (h *Handlers) setPending(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingCookie,
		Value:    value,
		Path:     statePath,
		HttpOnly: true,
		Secure:   h.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(pendingTTL.Seconds()),
	})
}

func (h *Handlers) clearPending(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingCookie,
		Value:    "",
		Path:     statePath,
		HttpOnly: true,
		Secure:   h.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (h *Handlers) clearState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    "",
		Path:     statePath,
		HttpOnly: true,
		Secure:   h.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// --- random -------------------------------------------------------------------

// token is a value nobody can guess: the state nonce, and the PKCE verifier.
func token() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand does not fail on any platform this runs on, and a
		// guessable nonce is worse than no sign-in at all.
		panic("oauth: no randomness available: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// safeNext keeps the browser on this site.
//
// The value ends up in a Location header, so anything that could name a host
// — an absolute URL, a protocol-relative "//elsewhere", a backslash some
// browsers read as a slash — is dropped rather than corrected.
//
// Control characters and backslashes are refused wherever they sit, not only
// at the front: a browser's URL parser deletes TAB, CR and LF from anywhere in
// a URL and reads a backslash as a slash, so "/<TAB>/evil.example" is
// "//evil.example" by the time it is followed. Checking the prefix of the raw
// string is checking something other than what the browser will read.
func safeNext(raw string) string {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return ""
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return ""
		}
	}
	return value
}
