package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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

func configureRisk(t *testing.T, in *instance, admin *session, service *httptest.Server, onLogin bool) {
	t.Helper()
	body := map[string]string{
		"risk.base_url":             service.URL,
		"risk.site":                 "arc-test",
		"risk.secret_key":           "rk-test-secret",
		"registration.captcha_mode": "risk",
	}
	if onLogin {
		body["risk.on_login"] = "true"
	}
	response := in.do(http.MethodPut, "/api/admin/settings", body, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("configure risk service: %d %s", response.Code, response.Body.String())
	}
}

func TestRiskControlGateOnRegistrationAndSignIn(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	stub := &riskStub{}
	service := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(service.Close)
	configureRisk(t, in, admin, service, false)

	// The front page now says so: the address and site key the browser's
	// SDK needs are public exactly while a check is on, and the secret
	// never is.
	site := in.do(http.MethodGet, "/api/site", nil, nil)
	if site.Code != http.StatusOK {
		t.Fatalf("GET /api/site: %d %s", site.Code, site.Body.String())
	}
	var info struct {
		RiskBaseURL  string `json:"risk_base_url"`
		RiskSite     string `json:"risk_site"`
		RiskOnSignup bool   `json:"risk_on_signup"`
		RiskOnLogin  bool   `json:"risk_on_login"`
	}
	if err := json.Unmarshal(site.Body.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal site: %v", err)
	}
	if info.RiskBaseURL != service.URL || info.RiskSite != "arc-test" || !info.RiskOnSignup || info.RiskOnLogin {
		t.Fatalf("site payload = %+v", info)
	}

	// And the backoffice reads the secret back only as a mask.
	configured := in.do(http.MethodGet, "/api/admin/settings", nil, admin)
	if configured.Code != http.StatusOK || strings.Contains(configured.Body.String(), "rk-test-secret") {
		t.Fatalf("the risk secret is readable out of the settings: %d %s", configured.Code, configured.Body.String())
	}

	// Register with no token at all: refused as a failed check, the same
	// shape a Turnstile token that never arrived gets.
	missing := in.do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "user1", "password": "valid-password",
	}, nil)
	if missing.Code != http.StatusForbidden || !strings.Contains(missing.Body.String(), "challenge_failed") {
		t.Fatalf("register without token: %d %s", missing.Code, missing.Body.String())
	}

	// A token the service judged: refused with the code that says there is
	// nothing to retry.
	blocked := in.do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "user2", "password": "valid-password", "rc_token": "rc-block",
	}, nil)
	if blocked.Code != http.StatusForbidden || !strings.Contains(blocked.Body.String(), "risk_blocked") {
		t.Fatalf("register with block verdict: %d %s", blocked.Code, blocked.Body.String())
	}

	// A token the service was not sure about: the account opens, but held
	// back the way an AI-reviewed sign-up is.
	challenged := in.do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "user3", "password": "valid-password", "rc_token": "rc-challenge",
	}, nil)
	if challenged.Code != http.StatusCreated {
		t.Fatalf("register with challenge verdict: %d %s", challenged.Code, challenged.Body.String())
	}
	var held struct {
		User struct {
			ID            string `json:"id"`
			APIRestricted bool   `json:"api_restricted"`
		} `json:"user"`
	}
	if err := json.Unmarshal(challenged.Body.Bytes(), &held); err != nil {
		t.Fatalf("unmarshal register: %v", err)
	}
	if !held.User.APIRestricted {
		t.Fatalf("a challenge-verdict account came through unrestricted: %s", challenged.Body.String())
	}

	// A token the service believed: straight in.
	allowed := in.do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "user4", "password": "valid-password", "rc_token": "rc-allow",
	}, nil)
	if allowed.Code != http.StatusCreated {
		t.Fatalf("register with allow verdict: %d %s", allowed.Code, allowed.Body.String())
	}
	var free struct {
		User struct {
			APIRestricted bool `json:"api_restricted"`
		} `json:"user"`
	}
	_ = json.Unmarshal(allowed.Body.Bytes(), &free)
	if free.User.APIRestricted {
		t.Fatalf("an allow-verdict account was restricted: %s", allowed.Body.String())
	}

	// Upstream compatibility: rcToken (camelCase) from sample implementations
	// is accepted just like rc_token.
	camelRegister := in.do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "user5", "password": "valid-password", "rcToken": "rc-allow",
	}, nil)
	if camelRegister.Code != http.StatusCreated {
		t.Fatalf("register with camelCase rcToken: %d %s", camelRegister.Code, camelRegister.Body.String())
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

	// Sign-in, with the operator's switch off so far: no token needed.
	signIn := in.do(http.MethodPost, "/api/auth/login", map[string]any{
		"identifier": "user4", "password": "valid-password",
	}, nil)
	if signIn.Code != http.StatusOK {
		t.Fatalf("login without the switch on: %d %s", signIn.Code, signIn.Body.String())
	}

	configureRisk(t, in, admin, service, true)

	accepted := in.do(http.MethodPost, "/api/auth/login", map[string]any{
		"identifier": "user4", "password": "valid-password", "rc_token": "rc-allow",
	}, nil)
	if accepted.Code != http.StatusOK {
		t.Fatalf("login with allow verdict: %d %s", accepted.Code, accepted.Body.String())
	}

	camelLogin := in.do(http.MethodPost, "/api/auth/login", map[string]any{
		"identifier": "user5", "password": "valid-password", "rcToken": "rc-allow",
	}, nil)
	if camelLogin.Code != http.StatusOK {
		t.Fatalf("login with camelCase rcToken: %d %s", camelLogin.Code, camelLogin.Body.String())
	}

	rejected := in.do(http.MethodPost, "/api/auth/login", map[string]any{
		"identifier": "user4", "password": "valid-password", "rc_token": "rc-block",
	}, nil)
	if rejected.Code != http.StatusForbidden || !strings.Contains(rejected.Body.String(), "risk_blocked") {
		t.Fatalf("login with block verdict: %d %s", rejected.Code, rejected.Body.String())
	}

	// The middle band does not hold at the door: a sign-in has no second
	// endpoint to hand a "maybe" to, so only "block" refuses.
	middle := in.do(http.MethodPost, "/api/auth/login", map[string]any{
		"identifier": "user4", "password": "valid-password", "rc_token": "rc-challenge",
	}, nil)
	if middle.Code != http.StatusOK {
		t.Fatalf("login with challenge verdict: %d %s", middle.Code, middle.Body.String())
	}
}

// Switching the service off takes the whole feature with it, including what
// /api/site advertises: a page that draws no challenge must not be handed a
// service address for one.
func TestRiskControlUnconfiguredIsInvisible(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	response := in.do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "plainuser", "password": "valid-password",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("register with no risk service configured: %d %s", response.Code, response.Body.String())
	}

	site := in.do(http.MethodGet, "/api/site", nil, nil)
	if site.Code != http.StatusOK {
		t.Fatalf("GET /api/site: %d %s", site.Code, site.Body.String())
	}
	var info map[string]any
	if err := json.Unmarshal(site.Body.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal site: %v", err)
	}
	if value, _ := info["risk_base_url"].(string); value != "" {
		t.Fatalf("site payload advertises an unconfigured service: %q", value)
	}
	if value, _ := info["risk_site"].(string); value != "" {
		t.Fatalf("site payload hands out a site key with no service: %q", value)
	}

	// The mode selects the service without the credentials behind it: the
	// challenge is not offered, so nobody is locked out of their own
	// instance by half a configuration.
	saved := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"registration.captcha_mode": "risk",
	}, admin)
	if saved.Code != http.StatusOK {
		t.Fatalf("select the risk mode: %d %s", saved.Code, saved.Body.String())
	}
	second := in.do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "plainuser2", "password": "valid-password",
	}, nil)
	if second.Code != http.StatusCreated {
		t.Fatalf("register with the service half-configured: %d %s", second.Code, second.Body.String())
	}
}
