package oauth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

// Carrying a sign-in across the trip to the provider and back.
//
// Nothing about a half-finished sign-in is stored. What has to survive the
// round trip — which provider, which browser, the PKCE verifier, and whether
// this is a new session or a link onto an account already signed in — goes
// into a cookie, signed with the instance secret so that it can be believed
// when it comes back.
//
// The signature proves this process wrote the value. It cannot prove that it
// was written in this browser: a cookie can be set by anything that has got
// onto a neighbouring hostname, and a valid value from another browser is a
// real attack. Plant your own half-finished sign-in in somebody else's browser
// and they come back signed in as you, typing into an account you can read.
// So when cookies are Secure they take the __Host- prefix, which a browser will
// not accept with a Domain attribute, so a neighbouring hostname cannot set
// them at all. A value this process did not sign is still noise.

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
	// Set only by connect, once the account has proved itself, so the callback
	// links the connection to that account rather than opening a session.
	UserID string `json:"u,omitempty"`
	// Where to send the browser afterwards. A path of this site, never a URL:
	// the whole value is put in a Location header, and one that could carry a
	// host would make this endpoint an open redirect with a provider's name
	// on it.
	Next string `json:"r,omitempty"`
	// Whether the sign-up challenge was checked and passed when this sign-in
	// started. A sign-in that opens an account is held to it while the operator
	// has the challenge on, so a state minted before this field existed decodes
	// as not passed and is held to it like any other sign-in that skipped it.
	SignUp bool `json:"g,omitempty"`
	// Whether the sign-in began on the register page. A sign-in that fails is
	// sent back to the page it began on, and the provider's redirect carries no
	// query to say which that was.
	Register bool  `json:"i,omitempty"`
	Expiry   int64 `json:"e"`
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
	Email string `json:"e"`
	Next  string `json:"r,omitempty"`
	// Carried from the state that started the sign-in. The form is where the
	// account is opened, and it has no state cookie of its own to ask.
	SignUp bool `json:"g,omitempty"`
	// The ticket's own id. The completion that opens a session spends it, so a
	// copy of the cookie cannot open a second session; a ticket with none was
	// issued before this field existed and is not honoured.
	Nonce  string `json:"i,omitempty"`
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

// maxPendingJSON is the most JSON a pending ticket may carry, so its cookie
// stays inside the browser's limit of 4096 bytes for name and value together.
// The name is 22 bytes with its __Host- prefix. The value is the base64 of the
// JSON, then a dot and a 43-byte tag: at 3000 bytes of JSON that is 4000 + 1 +
// 43, and the cookie comes to 4067 bytes. The JSON is the destination (at most
// 2048 bytes once escaped, see safeNext), about 130 bytes of fixed fields, and
// the identity the provider gave, which has no length of its own. A ticket that
// would not fit is refused, not sent with a cookie the browser would drop.
const maxPendingJSON = 3000

var errPendingTooLarge = errors.New("oauth: the sign-in's details do not fit in a cookie")

// encodeCookie is the JSON a signed cookie carries. HTML escaping is off because
// nothing here is written into a page, and with it on '<', '>' and '&' each grow
// to six bytes, so a 1 KB destination would be a 6 KB cookie. With it off the
// encoder still writes '"' as two bytes and U+2028 as six for three; safeNext
// refuses control characters, backslashes and invalid UTF-8, the only other
// things that grow, so the JSON is never more than twice the value it carries.
// Encode's trailing newline is not part of the value.
func encodeCookie(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func (s *stamp) issuePending(value pending) (string, error) {
	body, err := encodeCookie(value)
	if err != nil {
		return "", err
	}
	if len(body) > maxPendingJSON {
		return "", errPendingTooLarge
	}
	return secret.Sign(s.key, body), nil
}

func (s *stamp) readPending(cookie string) (pending, error) {
	body, ok := secret.Verify(s.key, cookie)
	if !ok {
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

// The state is bounded by safeNext's cap: the destination is at most 2048 bytes
// of JSON, and everything else is short and fixed, so the cookie stays near 3 KB,
// under the 4 KB the browser keeps. TestTheLongestDestinationFitsTheStateCookie
// holds that.
func (s *stamp) issue(value state) (string, error) {
	body, err := encodeCookie(value)
	if err != nil {
		return "", err
	}
	return secret.Sign(s.key, body), nil
}

func (s *stamp) read(cookie string) (state, error) {
	body, ok := secret.Verify(s.key, cookie)
	if !ok {
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

// --- the cookie ---------------------------------------------------------------

// cookieName is the name a sign-in cookie is set and read under. See the note
// at the top of this file for why Secure cookies carry the __Host- prefix.
func (h *Handlers) cookieName(base string) string {
	if h.SecureCookie {
		return "__Host-" + base
	}
	return base
}

// cookiePath is the path a sign-in cookie is scoped to. A __Host- cookie has to
// be sent for every path, so the Secure form gives up the narrower scope.
func (h *Handlers) cookiePath() string {
	if h.SecureCookie {
		return "/"
	}
	return statePath
}

func (h *Handlers) setState(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(stateCookie),
		Value:    value,
		Path:     h.cookiePath(),
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
		Name:     h.cookieName(pendingCookie),
		Value:    value,
		Path:     h.cookiePath(),
		HttpOnly: true,
		Secure:   h.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(pendingTTL.Seconds()),
	})
}

func (h *Handlers) clearPending(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(pendingCookie),
		Value:    "",
		Path:     h.cookiePath(),
		HttpOnly: true,
		Secure:   h.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (h *Handlers) clearState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(stateCookie),
		Value:    "",
		Path:     h.cookiePath(),
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

// maxNextBytes is the longest destination a sign-in carries. A consent request
// from another site, which is what a signed-out visitor is sent here with, runs
// to several hundred bytes, and a cap below that drops it after the provider has
// already approved, leaving the visitor on the home page.
const maxNextBytes = 1024

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
//
// The value rides in the signed state cookie, and a browser that drops a cookie
// over about 4 KB refuses the callback after the provider has approved the
// sign-in. Two things keep the cookie under that at the cap: encodeCookie does
// not HTML-escape, so the only growth left is '"' (two bytes) and U+2028 (six
// for three), at most doubling the value, and invalid UTF-8 is refused because
// the encoder would write each bad byte as a six-byte replacement character. So
// the destination is at most 2 KB of JSON, about 2.7 KB of base64.
func safeNext(raw string) string {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return ""
	}
	if len(value) > maxNextBytes {
		return ""
	}
	if !utf8.ValidString(value) {
		return ""
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return ""
		}
	}
	return value
}
