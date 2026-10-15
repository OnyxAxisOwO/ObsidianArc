package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usercheck"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func userCheckFixture(t *testing.T, client *http.Client) (*Handlers, *usercheck.Manager) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "admin-usercheck.db"),
		MaxOpenConns: 8, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	manager, err := usercheck.NewManager(ctx, db, []byte("test-instance-secret"), client)
	if err != nil {
		t.Fatalf("new UserCheck manager: %v", err)
	}
	return &Handlers{UserCheck: manager}, manager
}

func TestUserCheckConfigurationResponseOmitsAPIKey(t *testing.T) {
	h, manager := userCheckFixture(t, nil)
	cfg := manager.Config()
	if err := manager.Save(context.Background(), cfg, "usercheck-secret-do-not-return", false); err != nil {
		t.Fatalf("save API key: %v", err)
	}
	response := httptest.NewRecorder()
	if err := h.getUserCheck(response, httptest.NewRequest("GET", "/api/admin/usercheck", nil)); err != nil {
		t.Fatalf("get config: %v", err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := []string{"enabled", "exempt_domains", "failure_mode", "api_key_set"}
	got := make([]string, 0, len(body))
	for key := range body {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response fields = %v, want %v", got, want)
	}
	if body["api_key_set"] != true || strings.Contains(response.Body.String(), "usercheck-secret-do-not-return") {
		t.Fatalf("response disclosed key state incorrectly: %s", response.Body.String())
	}
}

func TestUserCheckPUTKeepsEmptyKeyAndExplicitlyClearsIt(t *testing.T) {
	h, manager := userCheckFixture(t, nil)
	ctx := context.Background()
	cfg := manager.Config()
	cfg.Enabled = false
	if err := manager.Save(ctx, cfg, "stored-usercheck-key", false); err != nil {
		t.Fatalf("save key: %v", err)
	}

	put := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/admin/usercheck", strings.NewReader(body))
		if err := h.putUserCheck(response, req); err != nil {
			t.Fatalf("put config: %v", err)
		}
		return response
	}
	kept := put(`{"enabled":false,"exempt_domains":["Example.org"],"failure_mode":"allow","api_key":"","clear_api_key":false}`)
	if kept.Code != http.StatusOK || !manager.Config().APIKeySet {
		t.Fatalf("empty key did not preserve the current key: status %d, body %s, config %+v", kept.Code, kept.Body.String(), manager.Config())
	}
	if !strings.Contains(kept.Body.String(), `"exempt_domains":["example.org"]`) {
		t.Fatalf("exempt domain was not normalized: %s", kept.Body.String())
	}
	cleared := put(`{"enabled":false,"exempt_domains":["example.org"],"failure_mode":"allow","api_key":"","clear_api_key":true}`)
	if cleared.Code != http.StatusOK || manager.Config().APIKeySet {
		t.Fatalf("explicit clear retained the key: status %d, body %s, config %+v", cleared.Code, cleared.Body.String(), manager.Config())
	}
}

func TestUserCheckTestAlwaysChecksExemptDomainsAndUsesCooldown(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q, want configured key", r.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"disposable":true}`)),
		}, nil
	})}
	h, manager := userCheckFixture(t, client)
	cfg := manager.Config()
	if err := manager.Save(context.Background(), cfg, "test-key", false); err != nil {
		t.Fatalf("save key: %v", err)
	}

	request := func() *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/usercheck/test", strings.NewReader(`{"email":"person@gmail.com"}`))
		httpx.Wrap(h.testUserCheck)(response, req)
		return response
	}
	first := request()
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"disposable":true`) || strings.Contains(first.Body.String(), `"skipped":true`) {
		t.Fatalf("first test response = %d %s", first.Code, first.Body.String())
	}
	second := request()
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second test status = %d, want cooldown; body %s", second.Code, second.Body.String())
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("UserCheck requests = %d, want exactly one paid lookup", got)
	}
}

// A refused key answered no lookup, so the operator must be able to test the
// corrected key straight away. The cooldown is only for paid lookups.
func TestUserCheckRefusedKeyDoesNotSpendTheTestCooldown(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		var requests atomic.Int32
		var answer atomic.Int32
		answer.Store(int32(status))
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			code := int(answer.Load())
			return &http.Response{
				StatusCode: code,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"disposable":false}`)),
			}, nil
		})}
		h, manager := userCheckFixture(t, client)
		if err := manager.Save(context.Background(), manager.Config(), "revoked-key", false); err != nil {
			t.Fatalf("save key: %v", err)
		}
		request := func() *httptest.ResponseRecorder {
			t.Helper()
			response := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/admin/usercheck/test", strings.NewReader(`{"email":"person@gmail.com"}`))
			httpx.Wrap(h.testUserCheck)(response, req)
			return response
		}

		refused := request()
		if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "refused") {
			t.Fatalf("HTTP %d from UserCheck: status %d, body %s", status, refused.Code, refused.Body.String())
		}
		answer.Store(http.StatusOK)
		retried := request()
		if retried.Code != http.StatusOK {
			t.Fatalf("HTTP %d then a corrected key: status %d, body %s, want the cooldown released", status, retried.Code, retried.Body.String())
		}
		if got := requests.Load(); got != 2 {
			t.Fatalf("HTTP %d: UserCheck requests = %d, want 2", status, got)
		}
	}
}

// The manager's sentinels carry a Go package prefix, and one of them tells
// the reader how to repair a stored key in code terms. Neither may reach a
// response body; the admin gets a sentence they can act on.
func TestUserCheckRefusalsDoNotSendTheGoErrorText(t *testing.T) {
	cases := map[string]struct {
		method, path, body string
		handler            func(*Handlers) func(http.ResponseWriter, *http.Request) error
		want               string
	}{
		"save enabled without a key": {
			method: http.MethodPut, path: "/api/admin/usercheck",
			body:    `{"enabled":true,"failure_mode":"reject","api_key":""}`,
			handler: func(h *Handlers) func(http.ResponseWriter, *http.Request) error { return h.putUserCheck },
			want:    "An API key is required when email screening is enabled.",
		},
		"save a key containing a line break": {
			method: http.MethodPut, path: "/api/admin/usercheck",
			body:    `{"enabled":false,"api_key":"abc\ndef"}`,
			handler: func(h *Handlers) func(http.ResponseWriter, *http.Request) error { return h.putUserCheck },
			want:    "The UserCheck settings are invalid.",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, _ := userCheckFixture(t, nil)
			response := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			httpx.Wrap(tc.handler(h))(response, req)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tc.want) {
				t.Fatalf("body %s does not carry %q", response.Body.String(), tc.want)
			}
			if strings.Contains(response.Body.String(), "usercheck:") {
				t.Fatalf("body carries the Go error text: %s", response.Body.String())
			}
		})
	}
}

// A stored key the server cannot open, as after a key-lost restore, is the one
// refusal the administrator can act on from here. The answer has to say so,
// because the screen's own wording for a 400 does not.
func TestUserCheckUnreadableKeyAnswersWithTheSentenceForTheAdministrator(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "admin-usercheck-unreadable.db"),
		MaxOpenConns: 8, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := usercheck.Config{Enabled: true, ExemptDomains: []string{}, FailureMode: usercheck.FailureReject}
	before, err := usercheck.NewManager(ctx, db, []byte("master-before-restore"), nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := before.Save(ctx, cfg, "sealed-under-the-old-secret", false); err != nil {
		t.Fatalf("save key: %v", err)
	}
	after, err := usercheck.NewManager(ctx, db, []byte("master-after-restore"), nil)
	if err != nil {
		t.Fatalf("boot under another secret: %v", err)
	}
	h := &Handlers{UserCheck: after}

	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/admin/usercheck",
		strings.NewReader(`{"enabled":true,"failure_mode":"reject","api_key":""}`))
	httpx.Wrap(h.putUserCheck)(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	want := "The stored UserCheck API key cannot be read. Enter a new key or clear it."
	if !strings.Contains(response.Body.String(), want) {
		t.Fatalf("body %s does not carry %q", response.Body.String(), want)
	}
}
