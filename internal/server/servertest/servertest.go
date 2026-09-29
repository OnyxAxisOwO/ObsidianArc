// Package servertest builds a whole server over a scratch SQLite database,
// for tests that live outside internal/server.
//
// A plugin is exactly such a test: what it changes is what the assembled
// server does over HTTP, and the only honest way to check that is to
// assemble one with the plugin compiled in. The plugin's test package
// imports the plugin (its own package, so its init has run and it is
// registered) and this, and every instance built here carries it — the same
// way the release binary would.
//
// internal/server's own tests keep their own harness. It predates this one,
// and they build servers with no plugins, which is the other half of the
// claim: the core works without any of them.
package servertest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server"
)

// Instance is one assembled server.
type Instance struct {
	T       *testing.T
	Handler http.Handler
	DB      *database.DB
	Server  *server.Server
}

// Session is a signed-in browser: its cookie, and the account it belongs to
// where the response said.
type Session struct {
	Cookie *http.Cookie
	UserID string
}

// New builds a server with every registered plugin set up and migrated.
// tweak adjusts the configuration for a test that needs one thing different.
func New(t *testing.T, tweak ...func(*config.Config)) *Instance {
	t.Helper()
	return NewSeeded(t, nil, tweak...)
}

// NewSeeded is New with settings rows written before the server is built —
// the state an instance is in when it boots with a configuration already in
// its database, which no request can put it in before the first account.
func NewSeeded(t *testing.T, seed map[string]string, tweak ...func(*config.Config)) *Instance {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Config{
		Addr:     ":0",
		DataDir:  dir,
		LogLevel: "error",
		Database: config.Database{
			Driver: "sqlite", DSN: filepath.Join(dir, "server.db"),
			MaxOpenConns: 4, MaxIdleConns: 4,
		},
		Session: config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
		// Deliberately cheap: nearly every case hashes a password, and the
		// cost function is not what is under test.
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
	if _, err := db.Migrate(ctx, plugin.Migrations()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for key, value := range seed {
		if _, err := db.Exec(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
			key, value, time.Now().UnixMilli()); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	app, err := server.New(ctx, server.Deps{Config: cfg, DB: db, Version: "test", Started: time.Now()})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return &Instance{T: t, Handler: app.Handler(), DB: db, Server: app}
}

// Do issues a request. A session sends its cookie; every unsafe method
// carries the same-origin header a browser would.
func (in *Instance) Do(method, path string, body any, as *Session) *httptest.ResponseRecorder {
	return in.DoWith(method, path, body, as, nil)
}

// DoWith is Do with extra request headers.
func (in *Instance) DoWith(method, path string, body any, as *Session, header http.Header) *httptest.ResponseRecorder {
	in.T.Helper()
	return in.do("", method, path, body, as, header)
}

// DoFrom is Do from a chosen client address — for the checks that count by
// address, which would otherwise see every test request arrive from one.
func (in *Instance) DoFrom(ip, method, path string, body any, as *Session) *httptest.ResponseRecorder {
	in.T.Helper()
	return in.do(ip, method, path, body, as, nil)
}

func (in *Instance) do(ip, method, path string, body any, as *Session, header http.Header) *httptest.ResponseRecorder {
	in.T.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			in.T.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request := httptest.NewRequest(method, path, reader)
	if ip != "" {
		request.RemoteAddr = ip + ":40000"
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if as != nil {
		request.AddCookie(as.Cookie)
	}

	recorder := httptest.NewRecorder()
	in.Handler.ServeHTTP(recorder, request)
	return recorder
}

// Register signs up through the public form and fails the test unless an
// account and a session came back. The first account on an instance is its
// administrator.
func (in *Instance) Register(username, password string) *Session {
	in.T.Helper()
	return in.RegisterWith(map[string]any{"username": username, "password": password})
}

// RegisterWith is Register with the whole request body chosen by the test.
func (in *Instance) RegisterWith(body map[string]any) *Session {
	in.T.Helper()
	response := in.Do(http.MethodPost, "/api/auth/register", body, nil)
	if response.Code != http.StatusCreated {
		in.T.Fatalf("register %v: %d %s", body["username"], response.Code, response.Body.String())
	}
	return in.sessionFrom(response)
}

// Login signs in with a password and fails the test unless a session came
// back.
func (in *Instance) Login(identifier, password string) *Session {
	in.T.Helper()
	response := in.Do(http.MethodPost, "/api/auth/login",
		map[string]any{"identifier": identifier, "password": password}, nil)
	if response.Code != http.StatusOK {
		in.T.Fatalf("login %s: %d %s", identifier, response.Code, response.Body.String())
	}
	return in.sessionFrom(response)
}

// SetSettings writes settings as an administrator would, failing the test on
// anything but success.
func (in *Instance) SetSettings(admin *Session, values map[string]string) {
	in.T.Helper()
	response := in.Do(http.MethodPut, "/api/admin/settings", values, admin)
	if response.Code != http.StatusOK {
		in.T.Fatalf("save settings %v: %d %s", values, response.Code, response.Body.String())
	}
}

func (in *Instance) sessionFrom(response *httptest.ResponseRecorder) *Session {
	in.T.Helper()
	var payload struct {
		User struct{ ID string } `json:"user"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "obsidian_session" && cookie.Value != "" {
			return &Session{Cookie: cookie, UserID: payload.User.ID}
		}
	}
	in.T.Fatalf("no session cookie in %d %s", response.Code, response.Body.String())
	return nil
}

// Decode reads a JSON response into T, failing the test if it is not one.
func Decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return out
}

// ErrorCode is the code of an error response, or "" for any other body.
func ErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return body.Error.Code
}
