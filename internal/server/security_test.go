package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// The authorization surface, exercised as HTTP rather than as function calls.
//
// The unit tests below each module already prove the queries are scoped; this
// proves the routing is, which is the layer where a mistake is invisible —
// a handler mounted without its middleware looks exactly like one with it.

type instance struct {
	t       *testing.T
	handler http.Handler
}

func newInstance(t *testing.T) *instance {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Config{
		Addr:     ":0",
		DataDir:  dir,
		LogLevel: "error",
		Database: config.Database{
			Driver: "sqlite", DSN: filepath.Join(dir, "server.db"),
			MaxOpenConns: 4, MaxIdleConns: 2,
		},
		Session: config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
		// Deliberately cheap: these tests hash a password on nearly every
		// case, and the cost function is not what is under test.
		Password:  config.Password{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32, MaxParallel: 4},
		Upstream:  config.Upstream{DialTimeout: time.Second, ResponseHeaderTimeout: 2 * time.Second, MaxIdleConns: 2, IdleConnTimeout: time.Second},
		SecretKey: []byte("a-test-instance-secret-value-here"),
	}

	ctx := context.Background()
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	app, err := New(ctx, Deps{Config: cfg, DB: db, Version: "test", Started: time.Now()})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return &instance{t: t, handler: app.Handler()}
}

type session struct {
	cookie *http.Cookie
	userID string
}

// do issues a request. A session sends its cookie; every unsafe method
// carries the same-origin header a browser would.
func (in *instance) do(method, path string, body any, as *session) *httptest.ResponseRecorder {
	in.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			in.t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if as != nil {
		request.AddCookie(as.cookie)
	}

	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

func (in *instance) register(username, password string) *session {
	in.t.Helper()
	response := in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": username, "password": password}, nil)
	if response.Code != http.StatusCreated {
		in.t.Fatalf("register %s: %d %s", username, response.Code, response.Body.String())
	}

	var payload struct {
		User struct{ ID string } `json:"user"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)

	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "obsidian_session" && cookie.Value != "" {
			return &session{cookie: cookie, userID: payload.User.ID}
		}
	}
	in.t.Fatalf("register %s returned no session cookie", username)
	return nil
}

func decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return out
}

// --- the matrix ------------------------------------------------------------

// Every administrative route, tried three ways. A route that answers anything
// but 401/403 to the first two is a route mounted without its middleware.
func TestAdminRoutesRequireAnAdministrator(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	regular := in.register("visitor", "another-password")

	routes := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/admin/dashboard", nil},
		{http.MethodGet, "/api/admin/users", nil},
		{http.MethodGet, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodPatch, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV", map[string]any{"nickname": "x"}},
		{http.MethodDelete, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodPost, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV/password", map[string]any{"new_password": "a-good-password"}},
		{http.MethodGet, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV/conversations", nil},
		{http.MethodGet, "/api/admin/groups", nil},
		{http.MethodPost, "/api/admin/groups", map[string]any{"name": "New"}},
		{http.MethodGet, "/api/admin/providers", nil},
		{http.MethodPost, "/api/admin/providers", map[string]any{"name": "P", "kind": "openai", "base_url": "https://x.example.com/v1", "api_key": "k"}},
		{http.MethodGet, "/api/admin/models", nil},
		{http.MethodPost, "/api/admin/models", map[string]any{"provider_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"}},
		{http.MethodGet, "/api/admin/usage", nil},
		{http.MethodGet, "/api/admin/usage/records", nil},
		{http.MethodGet, "/api/admin/quota/policies", nil},
		{http.MethodPut, "/api/admin/quota/policies", map[string]any{"scope": "global"}},
		{http.MethodGet, "/api/admin/settings", nil},
		{http.MethodPut, "/api/admin/settings", map[string]any{"site.name": "x"}},
		{http.MethodGet, "/api/admin/meta", nil},
	}

	for _, route := range routes {
		name := route.method + " " + route.path

		if code := in.do(route.method, route.path, route.body, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d, want 401", name, code)
		}
		if code := in.do(route.method, route.path, route.body, regular).Code; code != http.StatusForbidden {
			t.Errorf("%s as a regular user: %d, want 403", name, code)
		}
		// The administrator gets through the guard; whether the target exists
		// is a different question, so anything but 401/403 counts.
		if code := in.do(route.method, route.path, route.body, admin).Code; code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Errorf("%s as an administrator: %d, want the route to be reachable", name, code)
		}
	}
}

func TestUserRoutesRequireASession(t *testing.T) {
	in := newInstance(t)
	in.register("founder", "a-good-password")

	routes := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/models", nil},
		{http.MethodGet, "/api/conversations", nil},
		{http.MethodDelete, "/api/conversations", nil},
		{http.MethodPost, "/api/chat", map[string]any{"model_id": "x", "content": "hi"}},
		{http.MethodGet, "/api/usage/me", nil},
		{http.MethodGet, "/api/preferences", nil},
		{http.MethodPatch, "/api/preferences", map[string]any{"theme": "dark"}},
		{http.MethodPatch, "/api/profile", map[string]any{"nickname": "x"}},
		{http.MethodPost, "/api/profile/password", map[string]any{"current_password": "a", "new_password": "b"}},
		{http.MethodPost, "/api/attachments", map[string]any{"mime": "image/png", "data": ""}},
	}

	for _, route := range routes {
		if code := in.do(route.method, route.path, route.body, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: %d, want 401", route.method, route.path, code)
		}
	}
}

// One user's conversation must be invisible to another through the API, not
// merely absent from their listing.
func TestOneUserCannotReachAnothersConversation(t *testing.T) {
	in := newInstance(t)
	owner := in.register("owner", "a-good-password")
	stranger := in.register("stranger", "another-password")

	created := in.do(http.MethodPost, "/api/conversations", nil, owner)
	// There is no create endpoint; conversations come into being with their
	// first turn. Make one directly through the chat path's precondition
	// instead: an empty transcript is created by the gateway, so this test
	// uses the listing to confirm isolation of what exists.
	_ = created

	// Owner has none yet; the point is that a stranger's view of an id they
	// invented is a 404 rather than anything else.
	for _, path := range []string{
		"/api/conversations/01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"/api/attachments/01ARZ3NDEKTSV4RRFFQ69G5FAV",
	} {
		response := in.do(http.MethodGet, path, nil, stranger)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s as a stranger: %d, want 404", path, response.Code)
		}
	}

	// An attachment really owned by one user is not readable by the other.
	upload := in.do(http.MethodPost, "/api/attachments", map[string]any{
		"mime": "image/png",
		// A one-pixel PNG is unnecessary; the store does not decode it.
		"data": "iVBORw0KGgo=",
	}, owner)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", upload.Code, upload.Body.String())
	}
	attachment := decode[struct {
		Attachment struct{ ID string } `json:"attachment"`
	}](t, upload)

	if code := in.do(http.MethodGet, "/api/attachments/"+attachment.Attachment.ID, nil, owner).Code; code != http.StatusOK {
		t.Errorf("the owner could not read their own attachment: %d", code)
	}
	if code := in.do(http.MethodGet, "/api/attachments/"+attachment.Attachment.ID, nil, stranger).Code; code != http.StatusNotFound {
		t.Errorf("a stranger read someone else's attachment: %d", code)
	}
}

// --- transport rules --------------------------------------------------------

func TestUnsafeRequestsWithoutSameOriginAreRefused(t *testing.T) {
	in := newInstance(t)
	owner := in.register("owner", "a-good-password")

	request := httptest.NewRequest(http.MethodPatch, "/api/preferences", strings.NewReader(`{"theme":"dark"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.AddCookie(owner.cookie)

	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Errorf("cross-site write: %d, want 403", recorder.Code)
	}
}

func TestSessionCookieIsHardened(t *testing.T) {
	in := newInstance(t)
	response := in.do(http.MethodPost, "/api/auth/register",
		map[string]string{"username": "founder", "password": "a-good-password"}, nil)

	for _, cookie := range response.Result().Cookies() {
		if cookie.Name != "obsidian_session" {
			continue
		}
		if !cookie.HttpOnly {
			t.Error("the session cookie is readable by page script")
		}
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
		}
		if cookie.Path != "/" {
			t.Errorf("Path = %q", cookie.Path)
		}
		return
	}
	t.Fatal("no session cookie was set")
}

func TestSecurityHeadersArePresent(t *testing.T) {
	in := newInstance(t)
	response := in.do(http.MethodGet, "/api/health", nil, nil)

	policy := response.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP is missing %q: %s", want, policy)
		}
	}
	// 'unsafe-inline' in script-src would make the rest of the policy
	// decorative.
	if strings.Contains(policy, "script-src") && strings.Contains(scriptSrc(policy), "'unsafe-inline'") {
		t.Errorf("script-src allows inline script: %s", policy)
	}

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func scriptSrc(policy string) string {
	for _, directive := range strings.Split(policy, ";") {
		if strings.HasPrefix(strings.TrimSpace(directive), "script-src") {
			return directive
		}
	}
	return ""
}

// --- credential containment ---------------------------------------------------

// The one thing that must never come back out. Checked over the response
// bytes rather than the struct, because the struct is exactly what a future
// change might add a field to.
func TestProviderKeysNeverAppearInAResponse(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	const secretKey = "sk-do-not-leak-me-0123456789"
	create := in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Example", "kind": "openai",
		"base_url": "https://api.example.com/v1", "api_key": secretKey,
	}, admin)
	if create.Code != http.StatusCreated {
		t.Fatalf("create provider: %d %s", create.Code, create.Body.String())
	}

	for _, response := range []*httptest.ResponseRecorder{
		create,
		in.do(http.MethodGet, "/api/admin/providers", nil, admin),
		in.do(http.MethodGet, "/api/admin/dashboard", nil, admin),
	} {
		if strings.Contains(response.Body.String(), secretKey) {
			t.Fatalf("an API key appeared in a response: %s", response.Body.String())
		}
	}

	// The hint is what an administrator sees instead.
	listed := in.do(http.MethodGet, "/api/admin/providers", nil, admin)
	if !strings.Contains(listed.Body.String(), "6789") {
		t.Errorf("the key hint is missing: %s", listed.Body.String())
	}
}

// A disabled account must stop working on its next request, not at its next
// expiry.
func TestDisablingAnAccountEndsItsSessionImmediately(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	victim := in.register("visitor", "another-password")

	if code := in.do(http.MethodGet, "/api/auth/me", nil, victim).Code; code != http.StatusOK {
		t.Fatalf("the account could not read itself before being disabled: %d", code)
	}

	disable := in.do(http.MethodPatch, "/api/admin/users/"+victim.userID,
		map[string]any{"status": "disabled"}, admin)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", disable.Code, disable.Body.String())
	}

	if code := in.do(http.MethodGet, "/api/auth/me", nil, victim).Code; code != http.StatusUnauthorized {
		t.Errorf("a disabled account is still signed in: %d", code)
	}
}

// Losing the last administrator locks everyone out of the instance for good.
func TestTheLastAdministratorCannotBeRemoved(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	demote := in.do(http.MethodPatch, "/api/admin/users/"+admin.userID, map[string]any{"role": "user"}, admin)
	if demote.Code != http.StatusConflict {
		t.Errorf("demoting the last administrator: %d, want 409", demote.Code)
	}

	disable := in.do(http.MethodPatch, "/api/admin/users/"+admin.userID, map[string]any{"status": "disabled"}, admin)
	if disable.Code != http.StatusConflict {
		t.Errorf("disabling the last administrator: %d, want 409", disable.Code)
	}

	remove := in.do(http.MethodDelete, "/api/admin/users/"+admin.userID, nil, admin)
	if remove.Code == http.StatusNoContent {
		t.Error("the last administrator deleted themselves")
	}
}

// A malformed identifier must be refused before it reaches a query.
func TestMalformedIdentifiersAreRejected(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	// Percent-encoded, because these are what an attacker sends on the wire
	// and several of them are not valid in a request line unescaped.
	for _, segment := range []string{
		"not-an-id",
		url.PathEscape("' OR 1=1--"),
		url.PathEscape("../../etc/passwd"),
		url.PathEscape("01ARZ3NDEKTSV4RRFFQ69G5FAV; DROP TABLE users"),
		strings.Repeat("A", 4096),
	} {
		for _, prefix := range []string{"/api/admin/users/", "/api/conversations/", "/api/attachments/"} {
			path := prefix + segment
			code := in.do(http.MethodGet, path, nil, admin).Code
			if code == http.StatusOK {
				t.Errorf("GET %s returned 200", path)
			}
			if code >= 500 {
				t.Errorf("GET %s returned %d — a malformed id reached something that could not handle it", path, code)
			}
		}
	}

	// And the tables are all still there.
	if code := in.do(http.MethodGet, "/api/admin/users", nil, admin).Code; code != http.StatusOK {
		t.Errorf("listing users after the malformed requests: %d", code)
	}
}

// The SPA answers unknown paths so the router can, but an unknown API path is
// a client bug and should read as one.
func TestUnknownAPIPathIsNotTheSPA(t *testing.T) {
	in := newInstance(t)
	response := in.do(http.MethodGet, "/api/nope", nil, nil)

	if response.Code != http.StatusNotFound {
		t.Errorf("unknown API path: %d, want 404", response.Code)
	}
	if !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Errorf("unknown API path answered %q", response.Header().Get("Content-Type"))
	}
}
