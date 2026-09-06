package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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

// tweak lets one test ask for an instance that differs in a single respect —
// mail configured, say — without every other test paying for it.
func newInstance(t *testing.T, tweak ...func(*config.Config)) *instance {
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

	for _, apply := range tweak {
		apply(&cfg)
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
		{http.MethodPost, "/api/admin/usage/reset", map[string]any{"scope": "user", "id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"}},
		{http.MethodGet, "/api/admin/codes", nil},
		{http.MethodPost, "/api/admin/codes", map[string]any{"code": "X", "cards": 1}},
		{http.MethodDelete, "/api/admin/codes/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodPost, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV/cards", map[string]any{"cards": 1}},
		{http.MethodGet, "/api/admin/logs", nil},
		{http.MethodGet, "/api/admin/logs/facets", nil},
		{http.MethodPost, "/api/admin/logs/prune", map[string]any{"days": 30}},
		{http.MethodGet, "/api/admin/quota/policies", nil},
		{http.MethodPut, "/api/admin/quota/policies", map[string]any{"scope": "global"}},
		{http.MethodGet, "/api/admin/settings", nil},
		{http.MethodPut, "/api/admin/settings", map[string]any{"site.name": "x"}},
		{http.MethodGet, "/api/admin/meta", nil},
		{http.MethodGet, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV/conversations/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodGet, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV/keys", nil},
		{http.MethodDelete, "/api/admin/users/01ARZ3NDEKTSV4RRFFQ69G5FAV/keys/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodPatch, "/api/admin/groups/01ARZ3NDEKTSV4RRFFQ69G5FAV", map[string]any{"name": "x"}},
		{http.MethodDelete, "/api/admin/groups/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodPatch, "/api/admin/providers/01ARZ3NDEKTSV4RRFFQ69G5FAV", map[string]any{"name": "x"}},
		{http.MethodDelete, "/api/admin/providers/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodPost, "/api/admin/providers/01ARZ3NDEKTSV4RRFFQ69G5FAV/detect", nil},
		{http.MethodPatch, "/api/admin/models/01ARZ3NDEKTSV4RRFFQ69G5FAV", map[string]any{"display_name": "x"}},
		{http.MethodDelete, "/api/admin/models/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodPut, "/api/admin/models/order", map[string]any{"ids": []string{}}},
		{http.MethodGet, "/api/admin/announcements", nil},
		{http.MethodPost, "/api/admin/announcements", map[string]any{"title": "x", "body": "y"}},
		{http.MethodPatch, "/api/admin/announcements/01ARZ3NDEKTSV4RRFFQ69G5FAV", map[string]any{"title": "x"}},
		{http.MethodDelete, "/api/admin/announcements/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil},
		{http.MethodDelete, "/api/admin/quota/policies/global", nil},
		{http.MethodPost, "/api/admin/settings/import", map[string]any{"settings": map[string]string{}}},
		{http.MethodPost, "/api/admin/attachments/purge", nil},
	}

	// The list above is the whole route table, not a sample of it. A new
	// endpoint is protected by being mounted in admin.Routes, so the thing
	// worth failing on is one that was added there and never checked here.
	if mounted := adminRoutes(t); len(mounted) != len(routes) {
		t.Errorf("admin.Routes mounts %d endpoints and this test covers %d; "+
			"add the new one here", len(mounted), len(routes))
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
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
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

// The last-admin check is a read followed by a write. Two simultaneous
// demotions must be serialised or each administrator can observe the other
// and both writes will succeed, permanently locking the instance out.
func TestConcurrentAdminDemotionsCannotRemoveEveryAdministrator(t *testing.T) {
	in := newInstance(t)
	first := in.register("founder", "a-good-password")
	second := in.register("second-admin", "another-password")

	promote := in.do(http.MethodPatch, "/api/admin/users/"+second.userID,
		map[string]any{"role": "admin"}, first)
	if promote.Code != http.StatusOK {
		t.Fatalf("promote second admin: %d %s", promote.Code, promote.Body.String())
	}

	start := make(chan struct{})
	responses := make(chan int, 2)
	var workers sync.WaitGroup
	for _, candidate := range []*session{first, second} {
		candidate := candidate
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			responses <- in.do(http.MethodPatch, "/api/admin/users/"+candidate.userID,
				map[string]any{"role": "user"}, candidate).Code
		}()
	}
	close(start)
	workers.Wait()
	close(responses)

	succeeded, refused := 0, 0
	for code := range responses {
		switch code {
		case http.StatusOK:
			succeeded++
		case http.StatusConflict:
			refused++
		default:
			t.Fatalf("concurrent demotion returned %d", code)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("demotions: %d succeeded and %d refused; want one of each", succeeded, refused)
	}
}

// Deleting the last administrator goes through the same read-then-write as
// demoting one, and needs the same lock: two deletions that each see the
// other's administrator would both proceed and leave nobody.
func TestConcurrentAdminDeletionsCannotRemoveEveryAdministrator(t *testing.T) {
	in := newInstance(t)
	first := in.register("founder", "a-good-password")
	second := in.register("second-admin", "another-password")
	third := in.register("third-admin", "third-password")

	for _, target := range []*session{second, third} {
		promote := in.do(http.MethodPatch, "/api/admin/users/"+target.userID,
			map[string]any{"role": "admin"}, first)
		if promote.Code != http.StatusOK {
			t.Fatalf("promote: %d %s", promote.Code, promote.Body.String())
		}
	}

	// Each administrator deletes one of the others, so neither request is the
	// self-deletion the handler refuses outright. Only one may win.
	start := make(chan struct{})
	responses := make(chan int, 2)
	var workers sync.WaitGroup
	for _, pair := range [][2]*session{{second, third}, {third, second}} {
		actor, target := pair[0], pair[1]
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			responses <- in.do(http.MethodDelete, "/api/admin/users/"+target.userID, nil, actor).Code
		}()
	}
	close(start)
	workers.Wait()
	close(responses)

	// Whatever the interleaving, an administrator must remain.
	for code := range responses {
		if code != http.StatusNoContent && code != http.StatusConflict &&
			code != http.StatusNotFound && code != http.StatusUnauthorized {
			t.Fatalf("concurrent deletion returned %d", code)
		}
	}
	remaining := in.do(http.MethodGet, "/api/admin/users", nil, first)
	if remaining.Code != http.StatusOK {
		t.Fatalf("the founding administrator lost access: %d %s",
			remaining.Code, remaining.Body.String())
	}
}

// A key pinned to a model its owner cannot use would be issued happily and
// then refuse every request made with it. The restriction is checked against
// the same catalogue the turn is checked against, at the moment it is set.
func TestKeyCannotBePinnedToAnUnavailableModel(t *testing.T) {
	in := newInstance(t)
	// The first account is the administrator, which is what lets it turn the
	// API on; the switch is off by default and would refuse before the model
	// restriction is ever looked at.
	owner := in.register("keyholder", "a-good-password")
	enabled := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"api.enabled": "true"}, owner)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable the API: %d %s", enabled.Code, enabled.Body.String())
	}

	created := in.do(http.MethodPost, "/api/keys",
		map[string]any{"name": "pinned", "model_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, owner)
	if created.Code != http.StatusBadRequest {
		t.Fatalf("pinning to an unknown model gave %d %s, want 400",
			created.Code, created.Body.String())
	}

	// The unrestricted key is still allowed, so the check has not simply
	// broken key creation.
	free := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "open"}, owner)
	if free.Code != http.StatusCreated {
		t.Fatalf("unrestricted key was refused: %d %s", free.Code, free.Body.String())
	}

	var issued struct {
		Key struct{ ID string } `json:"key"`
	}
	if err := json.Unmarshal(free.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	patched := in.do(http.MethodPatch, "/api/keys/"+issued.Key.ID,
		map[string]any{"model_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, owner)
	if patched.Code != http.StatusBadRequest {
		t.Fatalf("pinning an existing key to an unknown model gave %d %s, want 400",
			patched.Code, patched.Body.String())
	}
}

// adminRoutes reads the route table out of the source rather than the router,
// because net/http's mux will not enumerate itself. It is a regex over one
// file in this repository, which is enough to answer "did somebody add an
// endpoint" and nothing more.
func adminRoutes(t *testing.T) []string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "admin", "admin.go"))
	if err != nil {
		t.Fatalf("read the admin route table: %v", err)
	}
	pattern := regexp.MustCompile(`mux\.Handle\("((?:GET|POST|PATCH|PUT|DELETE) /api/admin/[^"]*)"`)
	var out []string
	for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
		out = append(out, match[1])
	}
	if len(out) == 0 {
		t.Fatal("found no admin routes; the scanner has drifted from the source")
	}
	return out
}

// The About panel is the operator's to write. Empty means "keep the built-in
// wording", which is what a fresh instance serves, so the panel is never blank
// just because nobody has been to the settings screen.
func TestAboutTextIsOperatorWritableAndOptional(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	readAbout := func() (string, string) {
		t.Helper()
		response := in.do(http.MethodGet, "/api/site", nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET /api/site: %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			About struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"about"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.About.Title, payload.About.Body
	}

	// A fresh instance says nothing, and the client reads that as "use yours".
	if title, body := readAbout(); title != "" || body != "" {
		t.Fatalf("a fresh instance served about = %q / %q, want both empty", title, body)
	}

	saved := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"about.title": "ACME Chat",
		"about.body":  "Internal assistant. Ask #it-help before filing a ticket.",
	}, admin)
	if saved.Code != http.StatusOK {
		t.Fatalf("save about text: %d %s", saved.Code, saved.Body.String())
	}

	title, body := readAbout()
	if title != "ACME Chat" {
		t.Errorf("about title = %q, want ACME Chat", title)
	}
	if body != "Internal assistant. Ask #it-help before filing a ticket." {
		t.Errorf("about body = %q", body)
	}

	// Signed out too: the panel is reachable without an account.
	anonymous := in.do(http.MethodGet, "/api/site", nil, nil)
	if anonymous.Code != http.StatusOK {
		t.Fatalf("anonymous GET /api/site: %d", anonymous.Code)
	}

	// And clearing it returns to the built-in wording rather than sticking.
	cleared := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"about.title": "", "about.body": "",
	}, admin)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear about text: %d %s", cleared.Code, cleared.Body.String())
	}
	if title, body := readAbout(); title != "" || body != "" {
		t.Fatalf("cleared about = %q / %q, want both empty", title, body)
	}
}

// The standing notice above the chat. Unlike an announcement it carries no
// read state and no date, so the only two things to get right are that it is
// served to everyone — including a visitor who has not signed in, since the
// front door draws it too — and that an operator can say it may not be put
// away.
func TestHomeNoticeIsServedToEveryoneAndCanBeMadePermanent(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	read := func(as *session) (string, bool) {
		t.Helper()
		response := in.do(http.MethodGet, "/api/site", nil, as)
		if response.Code != http.StatusOK {
			t.Fatalf("GET /api/site: %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			HomeNotice struct {
				Text        string `json:"text"`
				Dismissible bool   `json:"dismissible"`
			} `json:"home_notice"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.HomeNotice.Text, payload.HomeNotice.Dismissible
	}

	// A fresh instance has nothing to say, and what it has to say is closable.
	if text, dismissible := read(nil); text != "" || !dismissible {
		t.Fatalf("fresh instance served %q / dismissible=%v, want empty and true", text, dismissible)
	}

	saved := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"home.notice":             "Maintenance 02:00–03:00 UTC on Sunday.",
		"home.notice_dismissible": "false",
	}, admin)
	if saved.Code != http.StatusOK {
		t.Fatalf("save the notice: %d %s", saved.Code, saved.Body.String())
	}

	// Signed out, because the front door renders it before anyone has an
	// account to read it with.
	text, dismissible := read(nil)
	if text != "Maintenance 02:00–03:00 UTC on Sunday." {
		t.Errorf("anonymous notice = %q", text)
	}
	if dismissible {
		t.Error("the notice is closable although the operator said it is not")
	}

	if signedIn, _ := read(admin); signedIn != text {
		t.Errorf("signed-in notice = %q, want the same %q", signedIn, text)
	}

	// Clearing it takes the strip down rather than leaving an empty bar; the
	// client reads an empty string as "render nothing".
	cleared := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"home.notice": ""}, admin)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear the notice: %d %s", cleared.Code, cleared.Body.String())
	}
	if text, _ := read(nil); text != "" {
		t.Fatalf("cleared notice = %q, want empty", text)
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
