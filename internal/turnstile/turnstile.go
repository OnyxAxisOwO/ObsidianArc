// Cloudflare Turnstile, for the places where an automated request creates
// something durable or crosses an operator-selected chat speed threshold.
//
// Not a general middleware. A challenge belongs on the handful of endpoints
// where the cost of an automated success is an account, a credential, or an
// unusually fast stream of provider calls, and
// putting one in front of everything would be a widget in the way of every
// reader on ordinary requests.
//
// The secret never leaves this process. The site key is public by design —
// it is in the page's markup — and is served with the rest of what the login
// page reads; the secret is write-only in the settings screen and redacted
// out of every response, the same shape a provider's API key has.
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Where Cloudflare checks a token. A constant rather than a setting: an
// operator who could point this at another host could point it at one that
// says yes to everything.
const verifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// How long to wait for Cloudflare. Longer than it should ever take and short
// enough that an outage there is a slow form rather than a hung one.
const timeout = 10 * time.Second

var (
	// The reader's token was missing, stale or already spent. Theirs to fix,
	// by trying the challenge again.
	ErrFailed = errors.New("turnstile: the challenge was not passed")
	// Cloudflare could not be reached or would not answer. Not the reader's
	// fault, and told apart from ErrFailed because the two deserve different
	// words and different status codes.
	ErrUnavailable = errors.New("turnstile: the challenge service is unavailable")
)

type response struct {
	Success bool     `json:"success"`
	Codes   []string `json:"error-codes"`
}

// Verify asks Cloudflare whether this token is good.
//
// The remote address is passed along where it is known: Cloudflare uses it to
// bind the token to the caller, which is what stops one solved challenge
// being resold to a thousand of them.
func Verify(ctx context.Context, client *http.Client, secret, token, ip string) error {
	return verifyAt(ctx, client, verifyURL, secret, token, ip)
}

// verifyAt is Verify with the endpoint as an argument, so the parsing and the
// error mapping can be tested against a local server. Unexported, and the
// only caller in the package passes the constant: an operator who could point this
// at another host could point it at one that says yes to everything.
func verifyAt(ctx context.Context, client *http.Client, endpoint, secret, token, ip string) error {
	if strings.TrimSpace(secret) == "" {
		// Nothing to check against. The caller decides whether that is a
		// misconfiguration or simply a challenge that is switched off; this
		// function will not quietly pass a request it did not verify.
		return ErrUnavailable
	}
	if strings.TrimSpace(token) == "" {
		return ErrFailed
	}

	form := url.Values{"secret": {secret}, "response": {token}}
	if ip != "" {
		form.Set("remoteip", ip)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if client == nil {
		client = http.DefaultClient
	}
	reply, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = reply.Body.Close() }()

	if reply.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrUnavailable, reply.StatusCode)
	}

	var body response
	if err := json.NewDecoder(reply.Body).Decode(&body); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if body.Success {
		return nil
	}

	// Two of Cloudflare's codes are about this instance's own configuration
	// rather than about the reader, and telling a visitor they failed a
	// challenge when the operator typed the secret wrong sends them round the
	// loop forever.
	for _, code := range body.Codes {
		switch code {
		case "invalid-input-secret", "missing-input-secret":
			return fmt.Errorf("%w: %s", ErrUnavailable, code)
		}
	}
	return ErrFailed
}

// Gate is the check as a call site needs it: whether it applies at all, and
// the secret to check against.
//
// Both are functions rather than values because they are settings an operator
// changes while the process runs, and a gate that captured them at boot would
// keep challenging after the switch was turned off. It also keeps the two
// packages that use this from having to know the setting keys.
type Gate struct {
	Client  *http.Client
	Enabled func() bool
	Secret  func() string
	// Test seams and private deployments may provide the verification call
	// directly. Production wiring leaves it nil and uses Cloudflare below.
	Verify func(context.Context, string, string) error
}

// Check passes silently when the challenge is switched off.
//
// A zero Gate is off, which is what makes it safe to leave unset in a test or
// a build that never wires it up.
func (g Gate) Check(ctx context.Context, token, ip string) error {
	if g.Enabled == nil || !g.Enabled() {
		return nil
	}
	if g.Verify != nil {
		return g.Verify(ctx, token, ip)
	}
	secret := ""
	if g.Secret != nil {
		secret = g.Secret()
	}
	return Verify(ctx, g.Client, secret, token, ip)
}
