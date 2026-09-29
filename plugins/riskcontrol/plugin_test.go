package riskcontrol

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
)

// The stub stands in for the self-hosted risk control service's
// /api/siteverify. It records what this instance sent and answers by the
// token, so a test scripts the verdict a token will earn.
type riskStub struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (s *riskStub) handler(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()

	verdict := map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}
	switch body["token"] {
	case "rc-allow":
		verdict = map[string]any{"success": true, "score": 0.9, "action": body["action"], "decision": "allow"}
	case "rc-challenge":
		verdict = map[string]any{"success": true, "score": 0.5, "action": body["action"], "decision": "challenge"}
	case "rc-block":
		verdict = map[string]any{"success": true, "score": 0.1, "action": body["action"], "decision": "block"}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(verdict)
}

func (s *riskStub) sent() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func configure(in *servertest.Instance, admin *servertest.Session, service *httptest.Server, onLogin bool) {
	body := map[string]string{
		BaseURL:                     service.URL,
		Site:                        "arc-test",
		SecretKey:                   "rk-test-secret",
		"registration.captcha_mode": CaptchaMode,
	}
	if onLogin {
		body[OnLogin] = "true"
	}
	in.SetSettings(admin, body)
}

// The guard's token rides in the request's "guards" object, under the
// plugin's name.
func withToken(body map[string]any, token string) map[string]any {
	body["guards"] = map[string]string{Name: token}
	return body
}

type siteBlock struct {
	Plugins map[string]struct {
		BaseURL  string `json:"base_url"`
		Site     string `json:"site"`
		OnSignup bool   `json:"on_signup"`
		OnLogin  bool   `json:"on_login"`
	} `json:"plugins"`
}

func TestRiskControlGateOnRegistrationAndSignIn(t *testing.T) {
	in := servertest.New(t)
	admin := in.Register("founder", "a-good-password")

	stub := &riskStub{}
	service := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(service.Close)
	configure(in, admin, service, false)

	// The front page now says so: the address and site key the browser's
	// SDK needs are public exactly while a check is on, and the secret
	// never is.
	site := in.Do(http.MethodGet, "/api/site", nil, nil)
	if site.Code != http.StatusOK {
		t.Fatalf("GET /api/site: %d %s", site.Code, site.Body.String())
	}
	info := servertest.Decode[siteBlock](t, site).Plugins[Name]
	if info.BaseURL != service.URL || info.Site != "arc-test" || !info.OnSignup || info.OnLogin {
		t.Fatalf("site payload = %+v", info)
	}
	if strings.Contains(site.Body.String(), "rk-test-secret") {
		t.Fatalf("the secret is in the public site payload: %s", site.Body.String())
	}

	// The policy lets the page load the service's SDK now, and nothing else
	// of it.
	policy := site.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self'") || !strings.Contains(policy, service.URL) {
		t.Fatalf("the service origin is not in the policy: %s", policy)
	}

	// And the backoffice reads the secret back only as a mask.
	configured := in.Do(http.MethodGet, "/api/admin/settings", nil, admin)
	if configured.Code != http.StatusOK || strings.Contains(configured.Body.String(), "rk-test-secret") {
		t.Fatalf("the risk secret is readable out of the settings: %d %s", configured.Code, configured.Body.String())
	}

	// Register with no token at all: refused as a failed check, the same
	// shape a Turnstile token that never arrived gets.
	missing := in.Do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "user1", "password": "valid-password",
	}, nil)
	if missing.Code != http.StatusForbidden || !strings.Contains(missing.Body.String(), "challenge_failed") {
		t.Fatalf("register without token: %d %s", missing.Code, missing.Body.String())
	}

	// A token the service judged: refused with the code that says there is
	// nothing to retry.
	blocked := in.Do(http.MethodPost, "/api/auth/register", withToken(map[string]any{
		"username": "user2", "password": "valid-password",
	}, "rc-block"), nil)
	if blocked.Code != http.StatusForbidden || !strings.Contains(blocked.Body.String(), "risk_blocked") {
		t.Fatalf("register with block verdict: %d %s", blocked.Code, blocked.Body.String())
	}

	// A token the service was not sure about: the account opens, but held
	// back the way an AI-reviewed sign-up is.
	challenged := in.Do(http.MethodPost, "/api/auth/register", withToken(map[string]any{
		"username": "user3", "password": "valid-password",
	}, "rc-challenge"), nil)
	if challenged.Code != http.StatusCreated {
		t.Fatalf("register with challenge verdict: %d %s", challenged.Code, challenged.Body.String())
	}
	type registered struct {
		User struct {
			APIRestricted bool `json:"api_restricted"`
		} `json:"user"`
	}
	if !servertest.Decode[registered](t, challenged).User.APIRestricted {
		t.Fatalf("a challenge-verdict account came through unrestricted: %s", challenged.Body.String())
	}

	// A token the service believed: straight in.
	allowed := in.Do(http.MethodPost, "/api/auth/register", withToken(map[string]any{
		"username": "user4", "password": "valid-password",
	}, "rc-allow"), nil)
	if allowed.Code != http.StatusCreated {
		t.Fatalf("register with allow verdict: %d %s", allowed.Code, allowed.Body.String())
	}
	if servertest.Decode[registered](t, allowed).User.APIRestricted {
		t.Fatalf("an allow-verdict account was restricted: %s", allowed.Body.String())
	}

	// What reached the service: the credentials of this instance, the
	// action the token was minted for, and the visitor's own address.
	for _, body := range stub.sent() {
		if body["site"] != "arc-test" || body["secret"] != "rk-test-secret" || body["action"] != "register" {
			t.Fatalf("siteverify request = %+v", body)
		}
		if ip, _ := body["remoteip"].(string); ip == "" {
			t.Fatalf("siteverify request carries no remoteip: %+v", body)
		}
	}

	// Refusals are in the security log, under the event they always were.
	events := in.Do(http.MethodGet, "/api/admin/security/events", nil, admin)
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), "risk_challenge") {
		t.Fatalf("refusals were not recorded: %d %s", events.Code, events.Body.String())
	}

	// Sign-in, with the operator's switch off so far: no token needed.
	signIn := in.Do(http.MethodPost, "/api/auth/login", map[string]any{
		"identifier": "user4", "password": "valid-password",
	}, nil)
	if signIn.Code != http.StatusOK {
		t.Fatalf("login without the switch on: %d %s", signIn.Code, signIn.Body.String())
	}

	configure(in, admin, service, true)

	accepted := in.Do(http.MethodPost, "/api/auth/login", withToken(map[string]any{
		"identifier": "user4", "password": "valid-password",
	}, "rc-allow"), nil)
	if accepted.Code != http.StatusOK {
		t.Fatalf("login with allow verdict: %d %s", accepted.Code, accepted.Body.String())
	}

	rejected := in.Do(http.MethodPost, "/api/auth/login", withToken(map[string]any{
		"identifier": "user4", "password": "valid-password",
	}, "rc-block"), nil)
	if rejected.Code != http.StatusForbidden || !strings.Contains(rejected.Body.String(), "risk_blocked") {
		t.Fatalf("login with block verdict: %d %s", rejected.Code, rejected.Body.String())
	}

	// The middle band does not hold at the door: a sign-in has no second
	// endpoint to hand a "maybe" to, so only "block" refuses.
	middle := in.Do(http.MethodPost, "/api/auth/login", withToken(map[string]any{
		"identifier": "user4", "password": "valid-password",
	}, "rc-challenge"), nil)
	if middle.Code != http.StatusOK {
		t.Fatalf("login with challenge verdict: %d %s", middle.Code, middle.Body.String())
	}
}

// Switching the service off takes the whole feature with it, including what
// /api/site advertises: a page that draws no challenge must not be handed a
// service address for one.
func TestRiskControlUnconfiguredIsInvisible(t *testing.T) {
	in := servertest.New(t)
	admin := in.Register("founder", "a-good-password")

	in.Register("plainuser", "valid-password")

	site := in.Do(http.MethodGet, "/api/site", nil, nil)
	info, advertised := servertest.Decode[siteBlock](t, site).Plugins[Name]
	if !advertised {
		t.Fatalf("a compiled-in plugin is not advertised: %s", site.Body.String())
	}
	if info.BaseURL != "" || info.Site != "" {
		t.Fatalf("site payload advertises an unconfigured service: %+v", info)
	}

	// The mode selects the service without the credentials behind it: the
	// challenge is not offered, so nobody is locked out of their own
	// instance by half a configuration.
	in.SetSettings(admin, map[string]string{"registration.captcha_mode": CaptchaMode})
	in.Register("plainuser2", "valid-password")
}

// The first account opens an empty instance, and no challenge stands in
// front of it — not even one configured through the database before anybody
// signed up.
func TestRiskControlNeverChallengesTheFirstAccount(t *testing.T) {
	stub := &riskStub{}
	service := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(service.Close)
	in := servertest.NewSeeded(t, map[string]string{
		BaseURL: service.URL, Site: "arc-test", SecretKey: "k",
		"registration.captcha_mode": CaptchaMode, OnLogin: "true",
	})

	info := servertest.Decode[siteBlock](t, in.Do(http.MethodGet, "/api/site", nil, nil)).Plugins[Name]
	if info.OnSignup || info.BaseURL != "" {
		t.Fatalf("an empty instance asks for a challenge: %+v", info)
	}
	in.Register("founder", "a-good-password")
	if len(stub.sent()) != 0 {
		t.Fatalf("the first account was sent to the service: %v", stub.sent())
	}
}

// An instance that configured the service before it could be switched keeps
// it: a stored setting is the trace the boot adopts it by. One that never
// touched it is offered the plugin, not given it.
func TestAConfiguredInstanceAdoptsThePlugin(t *testing.T) {
	configured := servertest.NewLegacy(t, map[string]string{Site: "arc-test"})
	founder := configured.Register("founder", "a-good-password")
	if state := pluginState(t, configured, founder); state != "enabled" {
		t.Fatalf("a configured instance's plugin is %s", state)
	}

	untouched := servertest.NewLegacy(t, nil)
	founder = untouched.Register("founder", "a-good-password")
	if state := pluginState(t, untouched, founder); state != "available" {
		t.Fatalf("an instance that never configured it has the plugin %s", state)
	}
}

// Switched off, the guard stands aside and the sign-up mode it answered for
// stops being one: the registration falls back to the core's challenge
// rather than to none, and the service is never asked.
func TestASwitchedOffGuardIsNotAsked(t *testing.T) {
	stub := &riskStub{}
	service := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(service.Close)
	in := servertest.New(t)
	admin := in.Register("founder", "a-good-password")
	configure(in, admin, service, true)
	authenticator := in.EnrolTwoFactor(admin)
	off := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/disable",
		map[string]any{"two_factor_code": authenticator.Next(t)}, admin)
	if off.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", off.Code, off.Body.String())
	}

	site := in.Do(http.MethodGet, "/api/site", nil, nil).Body.String()
	if strings.Contains(site, `"`+Name+`":`) {
		t.Fatalf("a switched-off plugin is advertised: %s", site)
	}
	in.Do(http.MethodPost, "/api/auth/login", withToken(map[string]any{
		"identifier": "founder", "password": "a-good-password"}, "rc-block"), nil)
	in.Do(http.MethodPost, "/api/auth/register", withToken(map[string]any{
		"username": "visitor", "password": "a-good-password"}, "rc-block"), nil)
	if len(stub.sent()) != 0 {
		t.Fatalf("a switched-off guard asked the service: %v", stub.sent())
	}
}

func pluginState(t *testing.T, in *servertest.Instance, as *servertest.Session) string {
	t.Helper()
	response := in.Do(http.MethodGet, "/api/admin/plugins/"+Name, nil, as)
	if response.Code != http.StatusOK {
		t.Fatalf("read the plugin: %d %s", response.Code, response.Body.String())
	}
	return servertest.Decode[struct {
		Plugin struct {
			State string `json:"state"`
		} `json:"plugin"`
	}](t, response).Plugin.State
}
