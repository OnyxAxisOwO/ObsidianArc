// Package riskcontrol is the client for a self-hosted anti-abuse service of
// the reCAPTCHA shape: the browser runs that service's SDK, which hands it a
// token, and this server trades the token for a verdict at /api/siteverify
// before letting the request through.
//
// It mirrors internal/turnstile deliberately — same Gate shape, same two
// sentinel errors — because from a call site's point of view the two are the
// same kind of check: an operator-selected challenge standing in front of
// the endpoints where an automated success creates something durable. What
// differs is the protocol (JSON rather than a form), the verdict (a score
// and a decision rather than a boolean) and that the service is the
// operator's own, so its address is a setting rather than a constant.
package riskcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// How long to wait for the verdict. The same bargain as the Turnstile
// client's: longer than the service should ever need, short enough that an
// outage there is a slow form rather than a hung one.
const timeout = 10 * time.Second

// What the service asks a token to stand for. Must match the action the
// browser's execute() was called with, or verification fails.
const (
	ActionRegister = "register"
	ActionLogin    = "login"
)

// The verdict's decision band. The service derives it from its own score
// thresholds; this server only ever chooses what to do with each band.
const (
	DecisionAllow     = "allow"
	DecisionChallenge = "challenge"
	DecisionBlock     = "block"
)

var (
	// The reader's token was missing, stale, already spent, or the service
	// judged it not a person. Theirs to fix, by trying again.
	ErrFailed = errors.New("riskcontrol: the risk check was not passed")
	// The service could not be reached or would not answer, or the
	// instance's own configuration is wrong. Not the reader's fault, and
	// told apart from ErrFailed because the two deserve different words
	// and different status codes.
	ErrUnavailable = errors.New("riskcontrol: the risk control service is unavailable")
)

// Result is the verdict for a token that the service accepted. Decision is
// one of the Decision constants — anything else reads as Allow, because an
// operator upgrading one side of the wire must not lock the other out.
type Result struct {
	Score    float64
	Decision string
}

type verifyRequest struct {
	Token    string `json:"token"`
	Action   string `json:"action"`
	Site     string `json:"site"`
	RemoteIP string `json:"remoteip,omitempty"`
	Secret   string `json:"secret"`
}

type verifyResponse struct {
	Success  bool     `json:"success"`
	Score    float64  `json:"score"`
	Action   string   `json:"action"`
	Decision string   `json:"decision"`
	Codes    []string `json:"error-codes"`
}

// Endpoint resolves the siteverify URL from the configured service base.
//
// An absolute base is used as it stands. A base that is only a path — the
// deployment that reverse-proxies the service under this instance's own
// domain, which the service's integration guide recommends as the simplest —
// is resolved against the instance's public URL, the origin a browser would
// have loaded boot.js from. Either part missing reads as unconfigured, not
// as an error: a half-filled form must not produce a URL this server would
// POST its secret to blindly.
func Endpoint(base, publicOrigin string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		return base + "/api/siteverify"
	}
	if !strings.HasPrefix(base, "/") {
		return ""
	}
	origin := strings.TrimRight(strings.TrimSpace(publicOrigin), "/")
	if origin == "" {
		return ""
	}
	return origin + base + "/api/siteverify"
}

// Verify asks the service whether this token is good.
//
// The remote address is passed along where it is known: the service checks
// that the address the browser finished its telemetry from is the address
// the token is finally submitted from, which is what stops a solve being
// resold through a relay. Only trusted when the site secret matches — see
// the service's own documentation.
func Verify(ctx context.Context, client *http.Client, endpoint, site, secret, token, action, ip string) (Result, error) {
	return verifyAt(ctx, client, endpoint, site, secret, token, action, ip)
}

func verifyAt(ctx context.Context, client *http.Client, endpoint, site, secret, token, action, ip string) (Result, error) {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(site) == "" || strings.TrimSpace(secret) == "" {
		// Nothing to check against. The caller decides whether that is a
		// misconfiguration or simply a challenge that is switched off; this
		// function will not quietly pass a request it did not verify.
		return Result{}, ErrUnavailable
	}
	// Overlong tokens cannot be genuine and waste connection buffers and downstream service memory.
	if strings.TrimSpace(token) == "" || len(token) > 65536 {
		return Result{}, ErrFailed
	}

	body, err := json.Marshal(verifyRequest{
		Token:    token,
		Action:   action,
		Site:     site,
		RemoteIP: ip,
		Secret:   secret,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json")

	if client == nil {
		client = http.DefaultClient
	}
	reply, err := client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = reply.Body.Close() }()

	// Read to a cap rather than trusting the service to be small: a verdict
	// is a hundred bytes, and a hostile one answering with a gigabyte must
	// not be able to spend this process's memory.
	raw, err := io.ReadAll(io.LimitReader(reply.Body, 64*1024))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if reply.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("%w: status %d", ErrUnavailable, reply.StatusCode)
	}

	var verdict verifyResponse
	if err := json.Unmarshal(raw, &verdict); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	if !verdict.Success {
		// The service's own configuration errors are this instance's
		// problem, not the reader's: telling a visitor they failed a check
		// when the operator pasted the wrong secret sends them round the
		// loop forever.
		for _, code := range verdict.Codes {
			switch code {
			case "invalid-input-secret", "missing-input-secret", "unknown-site":
				return Result{}, fmt.Errorf("%w: %s", ErrUnavailable, code)
			}
		}
		return Result{}, ErrFailed
	}

	// Action mismatch: a token minted for another action (e.g. login token
	// presented at register, or vice versa) is not valid for this verification.
	if verdict.Action != "" && verdict.Action != action {
		return Result{}, ErrFailed
	}

	decision := verdict.Decision
	switch decision {
	case DecisionAllow, DecisionChallenge, DecisionBlock:
	default:
		// A service this server does not understand yet is given the
		// benefit of the doubt; the score thresholds are the operator's
		// business and the decision band is theirs to define.
		decision = DecisionAllow
	}
	return Result{Score: verdict.Score, Decision: decision}, nil
}

// Gate is the check as a call site needs it: whether it applies at all, and
// the credentials and address to check against.
//
// All four are functions rather than values because they are settings an
// operator changes while the process runs, and a gate that captured them at
// boot would keep challenging after the switch was turned off.
type Gate struct {
	Client   *http.Client
	Enabled  func() bool
	Endpoint func() string
	Site     func() string
	Secret   func() string
	// Test seams and private deployments may provide the verification call
	// directly. Production wiring leaves it nil and uses Verify below.
	Verify func(ctx context.Context, token, action, ip string) (Result, error)
}

// Check passes silently — with a zero Result — when the check is switched
// off.
//
// A zero Gate is off, which is what makes it safe to leave unset in a test
// or a build that never wires it up.
func (g Gate) Check(ctx context.Context, token, action, ip string) (Result, error) {
	if g.Enabled == nil || !g.Enabled() {
		return Result{}, nil
	}
	if g.Verify != nil {
		return g.Verify(ctx, token, action, ip)
	}
	endpoint, site, secret := "", "", ""
	if g.Endpoint != nil {
		endpoint = g.Endpoint()
	}
	if g.Site != nil {
		site = g.Site()
	}
	if g.Secret != nil {
		secret = g.Secret()
	}
	return Verify(ctx, g.Client, endpoint, site, secret, token, action, ip)
}
