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
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
)

// A test run builds hundreds of servers around the same few plugins, and
// compiling one is most of a second. Nothing but tests imports this package.
func init() { wasm.ShareCompiledCode() }

// Instance is one assembled server.
type Instance struct {
	T       *testing.T
	Handler http.Handler
	DB      *database.DB
	Server  *server.Server

	config config.Config
}

// Session is a signed-in browser: its cookie, and the account it belongs to
// where the response said.
type Session struct {
	Cookie *http.Cookie
	UserID string
}

// New builds a server with every registered plugin installed and enabled.
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
	return build(t, installAll, seed, tweak)
}

// NewFresh builds a server on which no plugin has been installed — a new
// instance, where the plugins screen is the only way one arrives.
func NewFresh(t *testing.T, tweak ...func(*config.Config)) *Instance {
	t.Helper()
	return build(t, installNone, nil, tweak)
}

// NewPrepared builds a fresh server after prepare has been run against its
// database — migrated, with no plugin installed — for the tests that need an
// instance to be in some state before the server boots, and after it: a
// plugin an earlier build ran, a package the deployment ships.
func NewPrepared(t *testing.T, prepare func(*database.DB), tweak ...func(*config.Config)) *Instance {
	t.Helper()
	return buildWith(t, installNone, nil, tweak, prepare)
}

// NewLegacy builds the instance an upgrade meets: every plugin's migrations
// already run, and seed stored, from before plugins could be switched — and
// no record of any plugin's state, which the boot has to decide.
func NewLegacy(t *testing.T, seed map[string]string, tweak ...func(*config.Config)) *Instance {
	t.Helper()
	return build(t, installLegacy, seed, tweak)
}

type install int

const (
	installAll install = iota
	installNone
	installLegacy
)

func build(t *testing.T, mode install, seed map[string]string, tweak []func(*config.Config)) *Instance {
	t.Helper()
	return buildWith(t, mode, seed, tweak, nil)
}

func buildWith(t *testing.T, mode install, seed map[string]string, tweak []func(*config.Config), prepare func(*database.DB)) *Instance {
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
	var migrations []fs.FS
	if mode == installLegacy {
		migrations = plugin.Migrations()
	}
	if _, err := db.Migrate(ctx, migrations...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Recorded as installed before the server loads the states, which then
	// runs their migrations: the path an installed plugin takes at boot.
	if mode == installAll {
		for _, name := range plugin.Names() {
			if _, err := db.Exec(ctx,
				`INSERT INTO plugin_installs (name, state, updated_at) VALUES (?, 'enabled', ?)`,
				name, time.Now().UnixMilli()); err != nil {
				t.Fatalf("install %s: %v", name, err)
			}
		}
	}

	for key, value := range seed {
		if _, err := db.Exec(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
			key, value, time.Now().UnixMilli()); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	if prepare != nil {
		prepare(db)
	}
	app, err := server.New(ctx, server.Deps{Config: cfg, DB: db, Version: "test", Started: time.Now()})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return &Instance{T: t, Handler: app.Handler(), DB: db, Server: app, config: cfg}
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

// DoMultipart uploads one file as a browser's form would, for the endpoints
// that take one.
func (in *Instance) DoMultipart(method, path, field, filename string, data []byte, as *Session) *httptest.ResponseRecorder {
	in.T.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		in.T.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		in.T.Fatalf("multipart: %v", err)
	}
	if err := writer.Close(); err != nil {
		in.T.Fatalf("multipart: %v", err)
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if as != nil {
		request.AddCookie(as.Cookie)
	}
	recorder := httptest.NewRecorder()
	in.Handler.ServeHTTP(recorder, request)
	return recorder
}

// InstallPackage installs a plugin package the way the backoffice does: the
// file is uploaded and looked at, and then confirmed with the settings the
// operator chose. It fails the test on anything but success and returns the
// plugin as the confirmation answered.
func (in *Instance) InstallPackage(admin *Session, archive []byte, enable bool, values map[string]string) map[string]any {
	in.T.Helper()
	preview := in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "plugin.arcx", archive, admin)
	if preview.Code != http.StatusOK {
		in.T.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	token := Decode[struct {
		Preview struct{ Token string } `json:"preview"`
	}](in.T, preview).Preview.Token
	installed := in.Do(http.MethodPost, "/api/admin/plugins/install-package", map[string]any{
		"token": token, "enable": enable, "settings": values,
	}, admin)
	if installed.Code != http.StatusOK {
		in.T.Fatalf("install package: %d %s", installed.Code, installed.Body.String())
	}
	return Decode[struct {
		Plugin map[string]any `json:"plugin"`
	}](in.T, installed).Plugin
}

// Reboot builds a second server over the same database and data directory,
// the way a restart does: whatever the first one stored is what the second
// finds.
func (in *Instance) Reboot(tweak ...func(*config.Config)) *Instance {
	in.T.Helper()
	cfg := in.config
	for _, apply := range tweak {
		apply(&cfg)
	}
	app, err := server.New(context.Background(), server.Deps{Config: cfg, DB: in.DB, Version: "test", Started: time.Now()})
	if err != nil {
		in.T.Fatalf("reboot: %v", err)
	}
	return &Instance{T: in.T, Handler: app.Handler(), DB: in.DB, Server: app, config: cfg}
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

// TwoFactor is an account's authenticator as a test holds it: the secret,
// the step its enrolment spent, and the recovery codes, one per further
// confirmation a test needs — the authenticator refuses a step it has
// already accepted, and a test runs faster than thirty seconds.
type TwoFactor struct {
	Secret   string
	Step     int64
	Recovery []string
}

// Next is a code nobody has spent yet.
func (f *TwoFactor) Next(t *testing.T) string {
	t.Helper()
	if len(f.Recovery) == 0 {
		t.Fatal("out of recovery codes")
	}
	code := f.Recovery[0]
	f.Recovery = f.Recovery[1:]
	return code
}

// EnrolTwoFactor switches the second step on for a signed-in account.
func (in *Instance) EnrolTwoFactor(as *Session) *TwoFactor {
	in.T.Helper()
	setup := in.Do(http.MethodPost, "/api/profile/two-factor/setup", nil, as)
	if setup.Code != http.StatusOK {
		in.T.Fatalf("two-factor setup: %d %s", setup.Code, setup.Body.String())
	}
	secret := Decode[struct {
		Secret string `json:"secret"`
	}](in.T, setup).Secret
	step := totp.Step(time.Now())
	code, _ := totp.Code(secret, step)
	enable := in.Do(http.MethodPost, "/api/profile/two-factor/enable", map[string]string{"code": code}, as)
	if enable.Code != http.StatusOK {
		in.T.Fatalf("two-factor enable: %d %s", enable.Code, enable.Body.String())
	}
	codes := Decode[struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}](in.T, enable)
	return &TwoFactor{Secret: secret, Step: step, Recovery: codes.RecoveryCodes}
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
