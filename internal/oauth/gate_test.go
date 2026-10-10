package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// TestRequireForAllFollowsBothSwitches confirms RequireForAll is not just the
// policy setting on its own: an operator who turns the policy on and then
// switches OIDC itself off must not be left with every account holding at a
// screen with no button that could ever satisfy it.
func TestRequireForAllFollowsBothSwitches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if f.service.RequireForAll() {
		t.Fatal("RequireForAll should be false with nothing configured")
	}

	if err := f.settings.Set(ctx, settings.OAuthOIDCRequireForAll, "true"); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if f.service.RequireForAll() {
		t.Fatal("the policy alone, with OIDC itself off, should not require binding")
	}

	f.configure(t, "oidc")
	if err := f.settings.Set(ctx, settings.OAuthOIDCIssuer, "https://auth.example.com"); err != nil {
		t.Fatalf("set issuer: %v", err)
	}
	if !f.service.RequireForAll() {
		t.Fatal("the policy with OIDC configured and enabled should require binding")
	}

	if err := f.settings.Set(ctx, settings.OAuthOIDCEnabled, "false"); err != nil {
		t.Fatalf("disable oidc: %v", err)
	}
	if f.service.RequireForAll() {
		t.Fatal("switching the provider back off should lift the requirement")
	}
}

// TestMustBindOIDCClearsOnceConnected exercises the whole question the gate
// asks per request: on for an account with no OIDC connection, off once one
// exists, and never on at all when the policy itself is off.
func TestMustBindOIDCClearsOnceConnected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	member, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "member", Password: "another-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if must, err := f.service.MustBindOIDC(ctx, member); err != nil || must {
		t.Fatalf("must bind with the policy off = %v, %v; want false", must, err)
	}

	f.configure(t, "oidc")
	if err := f.settings.SetMany(ctx, map[string]string{
		settings.OAuthOIDCIssuer:        "https://auth.example.com",
		settings.OAuthOIDCRequireForAll: "true",
	}); err != nil {
		t.Fatalf("turn on the policy: %v", err)
	}

	must, err := f.service.MustBindOIDC(ctx, member)
	if err != nil || !must {
		t.Fatalf("must bind for an unconnected account under the policy = %v, %v; want true", must, err)
	}

	if err := f.service.Connect(ctx, member.ID, Identity{
		Provider: "oidc", Subject: "55512345", Login: "qq_55512345",
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if must, err := f.service.MustBindOIDC(ctx, member); err != nil || must {
		t.Fatalf("must bind after connecting = %v, %v; want false", must, err)
	}
}

// TestBindingGateHoldsAnAccountToTheConnectFlow puts the middleware itself
// under test, the way EnrolmentGate's own package tests would: an account
// this policy is holding reaches nothing but the allowed handful of routes,
// and once it has connected the same requests go through.
func TestBindingGateHoldsAnAccountToTheConnectFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.configure(t, "oidc")
	if err := f.settings.SetMany(ctx, map[string]string{
		settings.OAuthOIDCIssuer:        "https://auth.example.com",
		settings.OAuthOIDCRequireForAll: "true",
	}); err != nil {
		t.Fatalf("turn on the policy: %v", err)
	}

	member, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "member", Password: "another-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	gated := f.service.BindingGate()(next)

	request := func(method, path string) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(auth.WithUser(req.Context(), member))
		w := httptest.NewRecorder()
		gated.ServeHTTP(w, req)
		return w
	}

	held := request(http.MethodGet, "/api/conversations")
	if held.Code != http.StatusForbidden || !strings.Contains(held.Body.String(), "oidc_binding_required") || reached {
		t.Fatalf("an unconnected account reached a guarded route: %d %s", held.Code, held.Body.String())
	}

	for _, allowed := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodGet, "/api/site"},
		{http.MethodGet, "/api/auth/oauth/connections"},
		{http.MethodPost, "/api/auth/oauth/connections/oidc"},
		{http.MethodGet, "/api/auth/oauth/callback/oidc?code=c&state=x"},
	} {
		if w := request(allowed.method, allowed.path); !reached {
			t.Fatalf("%s %s was held by the gate: %d %s", allowed.method, allowed.path, w.Code, w.Body.String())
		}
	}

	// A GitHub connection would not satisfy this policy, and must not be let
	// through the gate the way the OIDC one above is. The navigation that used
	// to start a link is no longer a way to connect anything, so it is held too.
	for _, held := range []struct{ method, path string }{
		{http.MethodPost, "/api/auth/oauth/connections/github"},
		{http.MethodGet, "/api/auth/oauth/start/oidc"},
	} {
		if w := request(held.method, held.path); reached {
			t.Fatalf("%s %s reached past the gate: %d %s", held.method, held.path, w.Code, w.Body.String())
		}
	}

	if err := f.service.Connect(ctx, member.ID, Identity{
		Provider: "oidc", Subject: "55512345", Login: "qq_55512345",
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if w := request(http.MethodGet, "/api/conversations"); !reached || w.Code != http.StatusOK {
		t.Fatalf("a connected account is still held: %d %s", w.Code, w.Body.String())
	}
}

// A read error must fail an account through rather than lock every account
// out of the API on a database hiccup — the same choice Attach makes.
func TestBindingGateFailsOpenOnAStoreError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.configure(t, "oidc")
	if err := f.settings.SetMany(ctx, map[string]string{
		settings.OAuthOIDCIssuer:        "https://auth.example.com",
		settings.OAuthOIDCRequireForAll: "true",
	}); err != nil {
		t.Fatalf("turn on the policy: %v", err)
	}
	member, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "member", Password: "another-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Closing the database is the simplest way to make the lookup fail
	// without reaching into the store's own internals.
	_ = f.db.Close()

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	gated := f.service.BindingGate()(next)
	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	req = req.WithContext(auth.WithUser(req.Context(), member))
	w := httptest.NewRecorder()
	gated.ServeHTTP(w, req)
	if !reached {
		t.Fatalf("a store error locked the account out instead of failing open: %d %s", w.Code, w.Body.String())
	}
}
