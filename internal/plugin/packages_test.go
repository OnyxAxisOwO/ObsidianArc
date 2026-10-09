package plugin_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
)

// The demo plugin (sdk/examples/demo) is a real package: its backend is
// compiled to WebAssembly from Go source against the SDK and packed. These
// tests install it into whole servers and drive every extension point it
// uses over HTTP, which is the only place "the plugin works" can be checked.

const founderPassword = "a-good-password"

type admin struct {
	in *servertest.Instance
	s  *servertest.Session
}

func newAdmin(t *testing.T) admin {
	t.Helper()
	in := servertest.NewFresh(t)
	return admin{in: in, s: in.Register("founder", founderPassword)}
}

func (a admin) do(method, path string, body any) *httptest.ResponseRecorder {
	a.in.T.Helper()
	return a.in.Do(method, path, body, a.s)
}

func (a admin) mustDo(method, path string, body any, want int) *httptest.ResponseRecorder {
	a.in.T.Helper()
	res := a.do(method, path, body)
	if res.Code != want {
		a.in.T.Fatalf("%s %s: %d %s (want %d)", method, path, res.Code, res.Body.String(), want)
	}
	return res
}

func (a admin) setSettings(values map[string]string) { a.in.SetSettings(a.s, values) }

func (a admin) site() map[string]any {
	a.in.T.Helper()
	res := a.in.Do(http.MethodGet, "/api/site", nil, nil)
	return servertest.Decode[map[string]any](a.in.T, res)
}

func (a admin) pluginBlock() map[string]any {
	block, _ := a.site()["plugins"].(map[string]any)
	demo, _ := block["demo"].(map[string]any)
	return demo
}

func code(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	return servertest.ErrorCode(t, res)
}

// repack rewrites a package's manifest, keeping its files, for the tests
// that need the same plugin asking for something else.
func repack(t *testing.T, raw []byte, edit func(manifest map[string]any)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		if f.Name == "manifest.json" {
			var manifest map[string]any
			if err := json.Unmarshal(body, &manifest); err != nil {
				t.Fatal(err)
			}
			edit(manifest)
			body, _ = json.Marshal(manifest)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func listed(t *testing.T, a admin, name string) map[string]any {
	t.Helper()
	res := a.mustDo(http.MethodGet, "/api/admin/plugins", nil, http.StatusOK)
	body := servertest.Decode[struct {
		Plugins []map[string]any `json:"plugins"`
	}](t, res)
	for _, p := range body.Plugins {
		if p["name"] == name {
			return p
		}
	}
	return nil
}

func TestInstallingAPackageAsksBeforeItDoesAnything(t *testing.T) {
	a := newAdmin(t)
	raw := pkgtest.Demo(t)

	res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", raw, a.s)
	if res.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", res.Code, res.Body.String())
	}
	preview := servertest.Decode[struct {
		Preview struct {
			Token    string
			Action   string
			SHA256   string
			Warnings []string
			Manifest struct {
				Name        string
				Version     string
				Permissions []string
			}
		}
	}](t, res).Preview
	if preview.Action != "install" || preview.Manifest.Name != "demo" || preview.Token == "" || len(preview.SHA256) != 64 {
		t.Fatalf("preview = %+v", preview)
	}
	if len(preview.Manifest.Permissions) != 10 {
		t.Fatalf("permissions = %v", preview.Manifest.Permissions)
	}
	if strings.Join(preview.Warnings, ",") != "ui" {
		t.Fatalf("warnings = %v: a browser half runs in the page and the operator is told", preview.Warnings)
	}

	// Looked at, not installed: nothing of it exists yet.
	if listed(t, a, "demo") != nil {
		t.Fatal("a previewed package is listed as installed")
	}
	if code(t, a.do(http.MethodGet, "/api/admin/x/demo/things", nil)) != "not_found" && a.do(http.MethodGet, "/api/admin/x/demo/things", nil).Code != http.StatusNotFound {
		t.Fatal("a previewed package's route answers")
	}
	a.mustDo(http.MethodPut, "/api/admin/settings", map[string]string{"demo.mode": "closed"}, http.StatusBadRequest)
}

func TestAConfirmationNeedsTheUploadItConfirmsAndTheHandThatMadeIt(t *testing.T) {
	a := newAdmin(t)
	res := a.do(http.MethodPost, "/api/admin/plugins/install-package", map[string]any{"token": "nope", "enable": true})
	if res.Code != http.StatusBadRequest || code(t, res) != "upload_expired" {
		t.Fatalf("an unknown token: %d %s", res.Code, res.Body.String())
	}

	preview := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", pkgtest.Demo(t), a.s)
	token := servertest.Decode[struct {
		Preview struct{ Token string }
	}](t, preview).Preview.Token
	other := a.in.Register("second", founderPassword)
	if res := a.in.Do(http.MethodPost, "/api/admin/plugins/install-package", map[string]any{"token": token}, other); res.Code == http.StatusOK {
		t.Fatal("somebody else installed an upload they did not make")
	}
}

// A package's migrations run as the database's owner at install, whatever it
// declares, so the plugins_manage grant used to be a super administrator in
// waiting: upload a package whose migration promotes you. Uploading is now a
// super administrator's alone; switching what is installed stays delegable.
func TestADelegatedPluginManagerCannotUploadAPackage(t *testing.T) {
	a := newAdmin(t)
	delegate := a.in.Register("delegate", founderPassword)
	a.mustDo(http.MethodPatch, "/api/admin/users/"+delegate.UserID,
		map[string]any{"role": "admin", "admin_permissions": []string{plugin.PermissionView, plugin.PermissionManage}}, http.StatusOK)

	res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", pkgtest.Demo(t), delegate)
	if res.Code != http.StatusForbidden {
		t.Fatalf("a delegated manager's upload = %d %s, want it refused", res.Code, res.Body.String())
	}
	if res := a.in.Do(http.MethodGet, "/api/admin/plugins", nil, delegate); res.Code != http.StatusOK {
		t.Errorf("the delegate lost the plugins list: %d", res.Code)
	}
}

func TestAnUploadThatIsNotAPackageIsRefusedWithWhy(t *testing.T) {
	a := newAdmin(t)
	for label, raw := range map[string][]byte{
		"not a zip":   []byte("just some text"),
		"no manifest": repackEmpty(t),
	} {
		res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "x.arcx", raw, a.s)
		if res.Code != http.StatusBadRequest || code(t, res) != "invalid_package" {
			t.Errorf("%s: %d %s", label, res.Code, res.Body.String())
		}
	}
}

func repackEmpty(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	w, _ := zw.Create("readme.txt")
	_, _ = w.Write([]byte("hi"))
	_ = zw.Close()
	return out.Bytes()
}

func TestOnlyAnAdministratorWithTheGrantMayUploadAPackage(t *testing.T) {
	a := newAdmin(t)
	visitor := a.in.Register("visitor", founderPassword)
	res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", pkgtest.Demo(t), visitor)
	if res.Code != http.StatusForbidden && res.Code != http.StatusUnauthorized {
		t.Fatalf("a visitor could preview a package: %d", res.Code)
	}
	if res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", pkgtest.Demo(t), nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous preview: %d", res.Code)
	}
}

func TestAnInstalledButSwitchedOffPackageIsNotThere(t *testing.T) {
	a := newAdmin(t)
	info := a.in.InstallPackage(a.s, pkgtest.Demo(t), false, map[string]string{"demo.mode": "closed"})
	if info["state"] != "disabled" || info["kind"] != "package" || info["source"] != "upload" {
		t.Fatalf("installed as %v", info)
	}

	// Its settings are unknown, its route is not there, its guard is not
	// standing in front of anything, its browser half is not offered.
	if res := a.do(http.MethodPut, "/api/admin/settings", map[string]string{"demo.mode": "open"}); res.Code != http.StatusBadRequest {
		t.Fatalf("a switched-off plugin's setting was writable: %d", res.Code)
	}
	if res := a.do(http.MethodGet, "/api/admin/x/demo/things", nil); res.Code != http.StatusNotFound {
		t.Fatalf("its admin route: %d", res.Code)
	}
	if res := a.in.Do(http.MethodPost, "/api/x/demo/hook", map[string]string{"name": "x"}, nil); res.Code != http.StatusNotFound {
		t.Fatalf("its public route: %d", res.Code)
	}
	if a.pluginBlock() != nil {
		t.Fatal("/api/site advertises a switched-off plugin")
	}
	if res := a.in.Do(http.MethodGet, "/api/x/demo/web/ui.js", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("its browser half is served while it is off: %d", res.Code)
	}
	// The initial setting the operator chose is stored, and comes back when it is on.
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/enable", nil, http.StatusOK)
	res := a.mustDo(http.MethodGet, "/api/admin/settings", nil, http.StatusOK)
	settings := servertest.Decode[struct {
		Settings map[string]string `json:"settings"`
	}](t, res).Settings
	if settings["demo.mode"] != "closed" {
		t.Fatalf("the install's first settings were not kept: %v", settings["demo.mode"])
	}
}

func TestEnablingAPackageAttachesEverythingItBrought(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)

	// Site block and browser half, from what the backend said about itself.
	block := a.pluginBlock()
	if block == nil || block["mode"] != "open" || !strings.HasPrefix(block["_ui"].(string), "/api/x/demo/web/ui.js?v=") {
		t.Fatalf("the plugin's block of /api/site = %v", block)
	}
	ui := a.in.Do(http.MethodGet, block["_ui"].(string), nil, nil)
	if ui.Code != http.StatusOK || !strings.HasPrefix(ui.Header().Get("Content-Type"), "text/javascript") ||
		ui.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(ui.Body.String(), "export default") {
		t.Fatalf("browser half: %d %v", ui.Code, ui.Header())
	}
	if !strings.Contains(ui.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("a versioned address is not cached forever: %q", ui.Header().Get("Cache-Control"))
	}

	// A setting it defines is writable and validated by its manifest.
	a.mustDo(http.MethodPut, "/api/admin/settings", map[string]string{"demo.mode": "sideways"}, http.StatusBadRequest)
	a.setSettings(map[string]string{"demo.mode": "closed"})
	// ...and the site block follows it: the backend is asked again when a
	// setting of the plugin changes.
	if a.pluginBlock()["mode"] != "closed" || a.pluginBlock()["on_signup"] != true {
		t.Fatalf("the block did not follow the setting: %v", a.pluginBlock())
	}

	// Its guard now stands in front of sign-up.
	res := a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "newcomer", "password": founderPassword}, nil)
	if res.Code != http.StatusForbidden || code(t, res) != "demo_closed" {
		t.Fatalf("a sign-up with no token: %d %s", res.Code, res.Body.String())
	}
	res = a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "newcomer", "password": founderPassword, "guards": map[string]string{"demo": "block"},
	}, nil)
	if res.Code != http.StatusForbidden || code(t, res) != "demo_blocked" {
		t.Fatalf("a refused token: %d %s", res.Code, res.Body.String())
	}
	res = a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{
		"username": "newcomer", "password": founderPassword, "guards": map[string]string{"demo": "fine"},
	}, nil)
	if res.Code != http.StatusCreated {
		t.Fatalf("an accepted token: %d %s", res.Code, res.Body.String())
	}
}

// The generation lab is the third door a guard can stand in front of: a
// signed-in account spending provider money, asked before anything is paid
// for, and an administrator is asked like anybody else.
func TestAPackagesGuardStandsInFrontOfTheImageLab(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	visitor := a.in.Register("painter", founderPassword)
	a.setSettings(map[string]string{"demo.mode": "closed"})

	ask := func(token string, who *servertest.Session) *httptest.ResponseRecorder {
		body := map[string]any{"model_id": "01JNOSUCHMODEL0000000000000", "prompt": "a sunset"}
		if token != "" {
			body["guards"] = map[string]string{"demo": token}
		}
		return a.in.Do(http.MethodPost, "/api/images/generate", body, who)
	}
	res := ask("", visitor)
	if res.Code != http.StatusForbidden || code(t, res) != "demo_closed" {
		t.Fatalf("a picture with no token: %d %s", res.Code, res.Body.String())
	}
	res = ask("block", visitor)
	if res.Code != http.StatusForbidden || code(t, res) != "demo_blocked" {
		t.Fatalf("a refused token: %d %s", res.Code, res.Body.String())
	}
	// Past the guard the model does not exist, which is the answer that
	// shows nothing was in the way.
	if res = ask("fine", visitor); res.Code == http.StatusForbidden {
		t.Fatalf("an accepted token was refused: %d %s", res.Code, res.Body.String())
	}
	if res = ask("", a.s); res.Code != http.StatusForbidden || code(t, res) != "demo_closed" {
		t.Fatalf("the administrator was not asked for a token: %d %s", res.Code, res.Body.String())
	}
}

func TestAPackagesAccountFieldIsCheckedRequiredAndUnique(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.handle_rule": "required"})

	if fields, _ := a.site()["fields"].(map[string]any); fields["handle"] != "required" {
		t.Fatalf("/api/site fields = %v", fields)
	}
	register := func(username string, fields map[string]string) *httptest.ResponseRecorder {
		body := map[string]any{"username": username, "password": founderPassword}
		if fields != nil {
			body["fields"] = fields
		}
		return a.in.Do(http.MethodPost, "/api/auth/register", body, nil)
	}
	if res := register("one", nil); res.Code != http.StatusBadRequest || code(t, res) != "handle_required" {
		t.Fatalf("no handle: %d %s", res.Code, res.Body.String())
	}
	if res := register("one", map[string]string{"handle": "12ab"}); res.Code != http.StatusBadRequest || code(t, res) != "invalid_handle" {
		t.Fatalf("a bad handle: %d %s", res.Code, res.Body.String())
	}
	if res := register("one", map[string]string{"handle": "12345"}); res.Code != http.StatusCreated {
		t.Fatalf("a good handle: %d %s", res.Code, res.Body.String())
	}
	if res := register("two", map[string]string{"handle": "12345"}); res.Code != http.StatusConflict || code(t, res) != "handle_taken" {
		t.Fatalf("a taken handle: %d %s", res.Code, res.Body.String())
	}
}

func TestAPackagesAdminRoutesAreBehindTheBackofficesChecks(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	visitor := a.in.Register("visitor", founderPassword)

	if res := a.in.Do(http.MethodGet, "/api/admin/x/demo/things", nil, nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", res.Code)
	}
	if res := a.in.Do(http.MethodGet, "/api/admin/x/demo/things", nil, visitor); res.Code != http.StatusForbidden {
		t.Fatalf("a visitor: %d", res.Code)
	}

	// An administrator without the grant the route names is refused too.
	operator := a.in.Register("operator", founderPassword)
	a.mustDo(http.MethodPatch, "/api/admin/users/"+operator.UserID,
		map[string]any{"role": "admin", "admin_permissions": []string{"groups"}}, http.StatusOK)
	if res := a.in.Do(http.MethodGet, "/api/admin/x/demo/things", nil, operator); res.Code != http.StatusForbidden {
		t.Fatalf("an administrator without the grant: %d %s", res.Code, res.Body.String())
	}
	a.mustDo(http.MethodPatch, "/api/admin/users/"+operator.UserID,
		map[string]any{"role": "admin", "admin_permissions": []string{"users"}}, http.StatusOK)
	if res := a.in.Do(http.MethodGet, "/api/admin/x/demo/things", nil, operator); res.Code != http.StatusOK {
		t.Fatalf("an administrator with it: %d %s", res.Code, res.Body.String())
	}

	// A write, through the backend, in a transaction with a security event.
	created := a.mustDo(http.MethodPost, "/api/admin/x/demo/things", map[string]string{"name": "widget"}, http.StatusCreated)
	if !strings.Contains(created.Body.String(), `"name":"widget"`) {
		t.Fatalf("created = %s", created.Body.String())
	}
	if res := a.do(http.MethodPost, "/api/admin/x/demo/things", map[string]string{"name": "  "}); res.Code != http.StatusBadRequest || code(t, res) != "name_required" {
		t.Fatalf("a bad body: %d %s", res.Code, res.Body.String())
	}
	list := a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
	if !strings.Contains(list.Body.String(), "widget") || !strings.Contains(list.Body.String(), `"actor":"founder"`) {
		t.Fatalf("list = %s", list.Body.String())
	}
	events := a.mustDo(http.MethodGet, "/api/admin/security/events?limit=50", nil, http.StatusOK)
	if !strings.Contains(events.Body.String(), "demo_thing") {
		t.Fatalf("the backend's security event was not recorded: %s", events.Body.String())
	}
}

func TestAPublicRouteAuthenticatesItselfAndReachesTheDatabase(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)

	// No token configured: the endpoint answers nothing at all.
	if res := a.in.Do(http.MethodPost, "/api/x/demo/hook", map[string]string{"name": "x"}, nil); res.Code != http.StatusNotFound {
		t.Fatalf("unconfigured: %d", res.Code)
	}
	a.setSettings(map[string]string{"demo.secret": "s3cret-token"})
	hook := func(token string) *httptest.ResponseRecorder {
		return a.in.DoWith(http.MethodPost, "/api/x/demo/hook", map[string]string{"name": "from-bot"}, nil,
			http.Header{"Authorization": {"Bearer " + token}})
	}
	if res := hook("wrong"); res.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d", res.Code)
	}
	if res := hook("s3cret-token"); res.Code != http.StatusOK {
		t.Fatalf("the right token: %d %s", res.Code, res.Body.String())
	}
	list := a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
	if !strings.Contains(list.Body.String(), "from-bot") {
		t.Fatalf("the hook wrote nothing: %s", list.Body.String())
	}
	// The secret is write-only like every credential.
	res := a.mustDo(http.MethodGet, "/api/admin/settings", nil, http.StatusOK)
	if strings.Contains(res.Body.String(), "s3cret-token") {
		t.Fatal("a package's secret setting came back in a response")
	}
}

func TestATransactionLeftOpenByAFailureTakesEverythingBackWithIt(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	victim := a.in.Register("victim", founderPassword)

	failed := a.do(http.MethodPost, "/api/admin/x/demo/disable/"+victim.UserID, map[string]bool{"fail_after": true})
	if failed.Code != http.StatusConflict || code(t, failed) != "asked_to_fail" {
		t.Fatalf("the failing call: %d %s", failed.Code, failed.Body.String())
	}
	if res := a.in.Do(http.MethodGet, "/api/auth/me", nil, victim); res.Code != http.StatusOK {
		t.Fatalf("the account was disabled by a transaction that rolled back: %d", res.Code)
	}
	events := a.mustDo(http.MethodGet, "/api/admin/security/events?limit=50", nil, http.StatusOK)
	if strings.Contains(events.Body.String(), "demo_disable") {
		t.Fatal("a rolled-back transaction left its security event")
	}

	// And the same call without the failure does everything at once: status,
	// the sessions, an inbox entry, a log line.
	a.mustDo(http.MethodPost, "/api/admin/x/demo/disable/"+victim.UserID, map[string]bool{}, http.StatusOK)
	if res := a.in.Do(http.MethodGet, "/api/auth/me", nil, victim); res.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account's session still works: %d", res.Code)
	}
	events = a.mustDo(http.MethodGet, "/api/admin/security/events?limit=50", nil, http.StatusOK)
	if !strings.Contains(events.Body.String(), "demo_disable") {
		t.Fatal("the committed transaction left no security event")
	}
}

func TestABackendCanOnlyDoWhatItsManifestSaysItMay(t *testing.T) {
	a := newAdmin(t)
	noDB := repack(t, pkgtest.Demo(t), func(m map[string]any) {
		m["permissions"] = []string{"network"}
	})
	a.in.InstallPackage(a.s, noDB, true, nil)
	res := a.do(http.MethodGet, "/api/admin/x/demo/things", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("a query without the db permission: %d %s", res.Code, res.Body.String())
	}
	// Its own settings are always readable; that is not a permission.
	if a.pluginBlock()["mode"] != "open" {
		t.Fatalf("describe without db: %v", a.pluginBlock())
	}
}

func TestAPackageOutgoingRequestsNeedNetworkAndOriginsFollowSettings(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" && r.Header.Get("X-Demo") == "1" {
			_, _ = w.Write([]byte("pong"))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.upstream": upstream.URL})

	res := a.mustDo(http.MethodGet, "/api/x/demo/upstream", nil, http.StatusOK)
	if !strings.Contains(res.Body.String(), `"pong"`) || !strings.Contains(res.Body.String(), `"status":200`) {
		t.Fatalf("upstream = %s", res.Body.String())
	}

	// The page's policy trusts the origin the plugin asked for, while it asks.
	csp := func() string {
		return a.in.Do(http.MethodGet, "/", nil, nil).Header().Get("Content-Security-Policy")
	}
	if !strings.Contains(csp(), upstream.URL) || !strings.Contains(csp(), "blob:") {
		t.Fatalf("the policy does not carry the plugin's origin: %s", csp())
	}
	a.setSettings(map[string]string{"demo.upstream": ""})
	if strings.Contains(csp(), upstream.URL) {
		t.Fatal("the origin outlived the setting that asked for it")
	}
	a.setSettings(map[string]string{"demo.upstream": upstream.URL})
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{}, http.StatusOK)
	if strings.Contains(csp(), upstream.URL) {
		t.Fatal("a switched-off plugin's origin is still trusted")
	}

	// Without the permission the same request is refused.
	b := newAdmin(t)
	b.in.InstallPackage(b.s, repack(t, pkgtest.Demo(t), func(m map[string]any) { m["permissions"] = []string{"db"} }), true, nil)
	b.setSettings(map[string]string{"demo.upstream": upstream.URL})
	if res := b.do(http.MethodGet, "/api/x/demo/upstream", nil); res.Code != http.StatusInternalServerError {
		t.Fatalf("a fetch without the network permission: %d %s", res.Code, res.Body.String())
	}
}

func TestDisablingAndEnablingFollowsTheGateWithoutARestart(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.mode": "closed"})

	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{}, http.StatusOK)
	if res := a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "newcomer", "password": founderPassword}, nil); res.Code != http.StatusCreated {
		t.Fatalf("the guard stood in front of sign-up after the plugin was switched off: %d %s", res.Code, res.Body.String())
	}
	if res := a.do(http.MethodGet, "/api/admin/x/demo/things", nil); res.Code != http.StatusNotFound {
		t.Fatalf("its route after disabling: %d", res.Code)
	}
	if listed(t, a, "demo")["state"] != "disabled" {
		t.Fatal("the list does not say it is disabled")
	}

	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/enable", nil, http.StatusOK)
	if res := a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "another", "password": founderPassword}, nil); res.Code != http.StatusForbidden {
		t.Fatalf("the guard did not come back: %d", res.Code)
	}
	if a.pluginBlock()["mode"] != "closed" {
		t.Fatalf("the block after enabling: %v", a.pluginBlock())
	}
}

func TestRemovingAPackageIsRealAndItsDataStaysUnlessAskedOtherwise(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.mode": "closed"})
	a.mustDo(http.MethodPost, "/api/admin/x/demo/things", map[string]string{"name": "keepsake"}, http.StatusCreated)
	a.mustDo(http.MethodPatch, "/api/admin/users/"+a.s.UserID, map[string]any{"fields": map[string]string{"handle": "98765"}}, http.StatusOK)

	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/uninstall", map[string]any{"purge": false}, http.StatusOK)

	// Gone from the list, and from every extension point.
	if listed(t, a, "demo") != nil {
		t.Fatal("a removed package is still listed")
	}
	if res := a.do(http.MethodGet, "/api/admin/x/demo/things", nil); res.Code != http.StatusNotFound {
		t.Fatalf("its route: %d", res.Code)
	}
	if res := a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "newcomer", "password": founderPassword}, nil); res.Code != http.StatusCreated {
		t.Fatalf("its guard: %d %s", res.Code, res.Body.String())
	}
	a.mustDo(http.MethodPut, "/api/admin/settings", map[string]string{"demo.mode": "open"}, http.StatusBadRequest)
	if a.pluginBlock() != nil {
		t.Fatal("/api/site advertises a removed plugin")
	}
	var packages int
	if err := a.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM plugin_packages`).Scan(&packages); err != nil || packages != 0 {
		t.Fatalf("the archive is still stored: %d %v", packages, err)
	}

	// The data the operator did not ask to lose is where it was left.
	var rows int
	if err := a.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM demo_things`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("demo_things = %d %v", rows, err)
	}
	var handle string
	if err := a.in.DB.QueryRow(t.Context(), `SELECT handle FROM users WHERE id = ?`, a.s.UserID).Scan(&handle); err != nil || handle != "98765" {
		t.Fatalf("the account's field = %q %v", handle, err)
	}

	// Installing it again finds it all.
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	res := a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
	if !strings.Contains(res.Body.String(), "keepsake") {
		t.Fatalf("a reinstall did not find its data: %s", res.Body.String())
	}
	res = a.mustDo(http.MethodGet, "/api/admin/settings", nil, http.StatusOK)
	if servertest.Decode[struct{ Settings map[string]string }](t, res).Settings["demo.mode"] != "closed" {
		t.Fatal("a reinstall did not find its settings")
	}
}

func TestRemovingAPackageWithItsDataLeavesNothingAndTheServerCarriesOn(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.mode": "closed"})
	a.mustDo(http.MethodPatch, "/api/admin/users/"+a.s.UserID, map[string]any{"fields": map[string]string{"handle": "98765"}}, http.StatusOK)

	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/uninstall", map[string]any{"purge": true}, http.StatusOK)

	if _, err := a.in.DB.Query(t.Context(), `SELECT * FROM demo_things`); err == nil {
		t.Fatal("the plugin's table survived a purge")
	}
	if _, err := a.in.DB.Query(t.Context(), `SELECT handle FROM users`); err == nil {
		t.Fatal("the plugin's column survived a purge")
	}
	// Every account query still works without the column it used to name.
	a.mustDo(http.MethodGet, "/api/admin/users", nil, http.StatusOK)
	a.mustDo(http.MethodGet, "/api/auth/me", nil, http.StatusOK)
	if res := a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "newcomer", "password": founderPassword}, nil); res.Code != http.StatusCreated {
		t.Fatalf("sign-up after a purge: %d %s", res.Code, res.Body.String())
	}
	var stored int
	_ = a.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM settings WHERE key LIKE 'demo.%'`).Scan(&stored)
	if stored != 0 {
		t.Fatalf("%d setting rows survived a purge", stored)
	}

	// And it can be installed again, from nothing.
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
}

func TestARestartFindsWhatWasInstalledInTheDatabase(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.mode": "closed"})
	a.mustDo(http.MethodPost, "/api/admin/x/demo/things", map[string]string{"name": "before"}, http.StatusCreated)

	rebooted := admin{in: a.in.Reboot(), s: a.s}
	if rebooted.pluginBlock()["mode"] != "closed" {
		t.Fatalf("the block after a restart: %v", rebooted.pluginBlock())
	}
	list := rebooted.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
	if !strings.Contains(list.Body.String(), "before") {
		t.Fatalf("after a restart: %s", list.Body.String())
	}
	if res := rebooted.in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "late", "password": founderPassword}, nil); res.Code != http.StatusForbidden {
		t.Fatalf("the guard after a restart: %d", res.Code)
	}
	if info := listed(t, rebooted, "demo"); info["state"] != "enabled" || info["kind"] != "package" {
		t.Fatalf("after a restart: %v", info)
	}
}

func TestUploadingANewerVersionUpdatesInPlaceAndKeepsWhatTheOperatorSet(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.mode": "closed"})

	newer := repack(t, pkgtest.Demo(t), func(m map[string]any) { m["version"] = "1.1.0" })
	preview := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", newer, a.s)
	body := servertest.Decode[struct {
		Preview struct {
			Action   string
			Existing struct{ Version string }
		}
	}](t, preview)
	if body.Preview.Action != "update" || body.Preview.Existing.Version != "1.0.0" {
		t.Fatalf("preview = %+v", body.Preview)
	}
	info := a.in.InstallPackage(a.s, newer, true, nil)
	if info["state"] != "enabled" {
		t.Fatalf("an update changed the state: %v", info["state"])
	}
	if listed(t, a, "demo")["installed"].(map[string]any)["version"] != "1.1.0" {
		t.Fatalf("the version after an update: %v", listed(t, a, "demo"))
	}
	if a.pluginBlock()["mode"] != "closed" {
		t.Fatalf("an update lost the operator's setting: %v", a.pluginBlock())
	}
	if res := a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "newcomer", "password": founderPassword}, nil); res.Code != http.StatusForbidden {
		t.Fatalf("the guard after an update: %d", res.Code)
	}

	// The same file again is not an update.
	res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", newer, a.s)
	if servertest.Decode[struct{ Preview struct{ Action string } }](t, res).Preview.Action != "unchanged" {
		t.Fatalf("preview of an installed file: %s", res.Body.String())
	}
	token := servertest.Decode[struct{ Preview struct{ Token string } }](t, res).Preview.Token
	res = a.do(http.MethodPost, "/api/admin/plugins/install-package", map[string]any{"token": token})
	if res.Code != http.StatusConflict || code(t, res) != "plugin_unchanged" {
		t.Fatalf("installing an installed file: %d %s", res.Code, res.Body.String())
	}
}

func TestAPackageThatWouldCollideWithTheServerOrAnotherPackageIsRefusedBeforeAnythingChanges(t *testing.T) {
	a := newAdmin(t)
	refused := map[string]func(m map[string]any){
		"a core setting": func(m map[string]any) {
			m["settings"] = []map[string]any{{"key": "site.name"}}
			m["field_rules"] = nil
		},
		"a core route": func(m map[string]any) {
			m["routes"] = []map[string]any{{"pattern": "GET /api/admin/users", "access": "admin", "permission": "users"}}
		},
		"a core column": func(m map[string]any) {
			m["fields"] = []map[string]any{{"key": "email"}}
			m["field_rules"] = nil
			m["oauth_bindings"] = nil
		},
		"a core console command": func(m map[string]any) {
			m["console"] = []map[string]any{{"name": "user list", "summary": map[string]string{"en": "x"}}}
		},
		"a core sign-up mode": func(m map[string]any) { m["captcha_modes"] = []string{"turnstile"} },
	}
	for label, edit := range refused {
		raw := repack(t, pkgtest.Demo(t), func(m map[string]any) {
			edit(m)
		})
		res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", raw, a.s)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", label, res.Code, res.Body.String())
		}
	}
	if listed(t, a, "demo") != nil {
		t.Fatal("a refused package is listed")
	}

	// Two packages that want the same setting: the second is refused.
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	second := repack(t, pkgtest.Demo(t), func(m map[string]any) { m["name"] = "demotwo" })
	res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demotwo.arcx", second, a.s)
	if res.Code != http.StatusBadRequest || code(t, res) != "plugin_cannot_install" {
		t.Fatalf("a colliding second package: %d %s", res.Code, res.Body.String())
	}
}

func TestANonPortableMigrationIsRefusedAtInstall(t *testing.T) {
	a := newAdmin(t)
	raw := pkgtest.Demo(t)
	// SQLite-only SQL in a migration the other database would not run.
	bad := repackFile(t, raw, "migrations/demo_0001_things.sql", "CREATE TABLE demo_things (id INTEGER PRIMARY KEY AUTOINCREMENT);")
	res := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", bad, a.s)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("a non-portable migration: %d %s", res.Code, res.Body.String())
	}
}

func repackFile(t *testing.T, raw []byte, name, body string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if f.Name == name {
			data = []byte(body)
		}
		w, _ := zw.Create(f.Name)
		_, _ = w.Write(data)
	}
	_ = zw.Close()
	return out.Bytes()
}

func TestAMigrationThatFailsLeavesTheInstallUndone(t *testing.T) {
	a := newAdmin(t)
	broken := repackFile(t, pkgtest.Demo(t), "migrations/demo_0002_user_handle.sql", "ALTER TABLE no_such_table ADD COLUMN x TEXT;")
	preview := a.in.DoMultipart(http.MethodPost, "/api/admin/plugins/preview", "file", "demo.arcx", broken, a.s)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	token := servertest.Decode[struct{ Preview struct{ Token string } }](t, preview).Preview.Token
	res := a.do(http.MethodPost, "/api/admin/plugins/install-package", map[string]any{"token": token, "enable": true})
	if res.Code == http.StatusOK {
		t.Fatal("a package whose migration fails was installed")
	}
	if listed(t, a, "demo") != nil {
		t.Fatal("the failed install is listed")
	}
	// Nothing of it exists: not its table (the first migration ran in the
	// same transaction), not its route, not its setting.
	if _, err := a.in.DB.Query(t.Context(), `SELECT * FROM demo_things`); err == nil {
		t.Fatal("the failed install left a table behind")
	}
	if res := a.do(http.MethodGet, "/api/admin/x/demo/things", nil); res.Code != http.StatusNotFound {
		t.Fatalf("the failed install's route: %d", res.Code)
	}
	a.mustDo(http.MethodPut, "/api/admin/settings", map[string]string{"demo.mode": "closed"}, http.StatusBadRequest)
}

// consoleLine runs one console line as the session and returns what the
// terminal would print.
func consoleLine(t *testing.T, a admin, line string) (string, bool) {
	t.Helper()
	res := a.mustDo(http.MethodPost, "/api/console/exec", map[string]any{"line": line, "cols": 100}, http.StatusOK)
	var out strings.Builder
	ok := false
	for _, frame := range strings.Split(res.Body.String(), "\n\n") {
		var event, data string
		for _, l := range strings.Split(strings.TrimRight(frame, "\n"), "\n") {
			switch {
			case strings.HasPrefix(l, "event: "):
				event = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				data = strings.TrimPrefix(l, "data: ")
			}
		}
		switch event {
		case "out":
			var payload struct{ Text string }
			_ = json.Unmarshal([]byte(data), &payload)
			out.WriteString(payload.Text)
		case "done":
			var done struct{ OK bool }
			_ = json.Unmarshal([]byte(data), &done)
			ok = done.OK
		}
	}
	return out.String(), ok
}

func TestAPackagesConsoleCommandRunsItsBackendAndCallsTheAdminAPI(t *testing.T) {
	a := newAdmin(t)
	if out, ok := consoleLine(t, a, "demo things"); ok {
		t.Fatalf("a command of a plugin that is not installed ran: %s", out)
	}
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.mustDo(http.MethodPost, "/api/admin/x/demo/things", map[string]string{"name": "widget"}, http.StatusCreated)

	out, ok := consoleLine(t, a, "demo things --limit 5")
	if !ok || !strings.Contains(out, "widget") || !strings.Contains(out, "name") {
		t.Fatalf("demo things: ok=%v\n%q", ok, out)
	}
	t.Logf("console output: %q", out)
	if help, _ := consoleLine(t, a, "help demo things"); !strings.Contains(help, "List the demo plugin") {
		t.Fatalf("the command's help: %s", help)
	}

	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{}, http.StatusOK)
	if out, ok := consoleLine(t, a, "demo things"); ok {
		t.Fatalf("a switched-off plugin's command ran: %s", out)
	}
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/enable", nil, http.StatusOK)
	if _, ok := consoleLine(t, a, "demo things"); !ok {
		t.Fatal("the command did not come back")
	}
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/uninstall", map[string]any{"purge": false}, http.StatusOK)
	if out, ok := consoleLine(t, a, "demo things"); ok {
		t.Fatalf("a removed plugin's command ran: %s", out)
	}
}

func TestAnInviteeListIsDecoratedByThePackage(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"invites.user_enabled": "true", "invites.user_limit": "5"})
	inviter := a.in.Register("inviter", founderPassword)

	res := a.in.Do(http.MethodGet, "/api/profile/invites", nil, inviter)
	if res.Code != http.StatusOK {
		t.Fatalf("the invite panel: %d %s", res.Code, res.Body.String())
	}
	inviteCode := servertest.Decode[struct{ Code string }](t, res).Code
	if inviteCode == "" {
		t.Fatalf("no personal code: %s", res.Body.String())
	}
	a.in.RegisterWith(map[string]any{"username": "invitee", "password": founderPassword, "invite_code": inviteCode})

	res = a.in.Do(http.MethodGet, "/api/profile/invites", nil, inviter)
	body := servertest.Decode[struct {
		Invitees []map[string]any
	}](t, res)
	if len(body.Invitees) != 1 || body.Invitees[0]["demo"] != "hello invitee" {
		t.Fatalf("the package did not decorate the list: %s", res.Body.String())
	}
	// Switched off, the list is the core's own.
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{}, http.StatusOK)
	res = a.in.Do(http.MethodGet, "/api/profile/invites", nil, inviter)
	if strings.Contains(res.Body.String(), "hello invitee") {
		t.Fatal("a switched-off plugin decorated the list")
	}
}

// --- what the deployment ships with ---------------------------------------

func writeBundle(t *testing.T, dir string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "demo.arcx"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func withPluginDir(dir string) func(*config.Config) {
	return func(c *config.Config) { c.PluginDir = dir }
}

func TestABundledPackageOnAFreshInstanceIsInstalledSwitchedOff(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir, pkgtest.Demo(t))
	in := servertest.NewPrepared(t, func(*database.DB) {}, withPluginDir(dir))
	a := admin{in: in, s: in.Register("founder", founderPassword)}

	info := listed(t, a, "demo")
	if info == nil || info["state"] != "disabled" || info["source"] != "bundled" {
		t.Fatalf("a bundled package on a fresh instance: %v", info)
	}
	if res := a.do(http.MethodGet, "/api/admin/x/demo/things", nil); res.Code != http.StatusNotFound {
		t.Fatalf("a switched-off bundled package's route: %d", res.Code)
	}
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/enable", nil, http.StatusOK)
	a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)

	// What the operator removes stays removed across a restart and a deploy.
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/uninstall", map[string]any{"purge": false}, http.StatusOK)
	again := admin{in: in.Reboot(), s: a.s}
	if listed(t, again, "demo") != nil {
		t.Fatal("a package the operator removed came back with the next boot")
	}
}

func TestAPluginAnEarlierBuildRanIsTakenOverWithItsStateAndData(t *testing.T) {
	raw := pkgtest.Demo(t)
	pkg, err := arcx.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeBundle(t, dir, raw)

	// The instance as an older build left it: the plugin's tables and column
	// there, its state on record as enabled, its settings stored — and no
	// package anywhere.
	in := servertest.NewPrepared(t, func(db *database.DB) {
		ctx := context.Background()
		if _, err := db.Migrate(ctx, pkg.Migrations()); err != nil {
			t.Fatal(err)
		}
		must := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		must(`INSERT INTO plugin_installs (name, state, version, installed_at, updated_at) VALUES ('demo', 'enabled', '0.9.0', 1, 1)`)
		must(`INSERT INTO settings (key, value, updated_at) VALUES ('demo.mode', 'closed', 1)`)
		must(`INSERT INTO demo_things (id, name, created_at) VALUES ('t1', 'inherited', 1)`)
	}, withPluginDir(dir))
	a := admin{in: in, s: in.Register("founder", founderPassword)}

	info := listed(t, a, "demo")
	if info == nil || info["state"] != "enabled" || info["kind"] != "package" || info["source"] != "bundled" {
		t.Fatalf("after the take-over: %v", info)
	}
	// Running from the first request: the guard stands in front of sign-up
	// with the setting the old build stored, and the old data is served.
	if res := in.Do(http.MethodPost, "/api/auth/register", map[string]any{"username": "somebody", "password": founderPassword}, nil); res.Code != http.StatusForbidden {
		t.Fatalf("the guard after a take-over: %d %s", res.Code, res.Body.String())
	}
	list := a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
	if !strings.Contains(list.Body.String(), "inherited") {
		t.Fatalf("the data an earlier build wrote: %s", list.Body.String())
	}
}

func TestADeploymentUpdatesWhatItShippedAndLeavesWhatTheOperatorInstalled(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir, pkgtest.Demo(t))
	in := servertest.NewPrepared(t, func(*database.DB) {}, withPluginDir(dir))
	a := admin{in: in, s: in.Register("founder", founderPassword)}
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/enable", nil, http.StatusOK)

	newer := repack(t, pkgtest.Demo(t), func(m map[string]any) { m["version"] = "1.4.0" })
	writeBundle(t, dir, newer)
	a = admin{in: in.Reboot(), s: a.s}
	info := listed(t, a, "demo")
	if info["installed"].(map[string]any)["version"] != "1.4.0" || info["state"] != "enabled" {
		t.Fatalf("a newer bundled package did not update the installed one, or changed its state: %v", info)
	}

	// An older one does not downgrade it.
	writeBundle(t, dir, repack(t, pkgtest.Demo(t), func(m map[string]any) { m["version"] = "1.0.0" }))
	a = admin{in: a.in.Reboot(), s: a.s}
	if listed(t, a, "demo")["installed"].(map[string]any)["version"] != "1.4.0" {
		t.Fatal("an older bundled package downgraded the installed one")
	}

	// What the operator uploaded themselves is theirs.
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/uninstall", map[string]any{"purge": false}, http.StatusOK)
	a.in.InstallPackage(a.s, repack(t, pkgtest.Demo(t), func(m map[string]any) { m["version"] = "2.0.0" }), true, nil)
	writeBundle(t, dir, repack(t, pkgtest.Demo(t), func(m map[string]any) { m["version"] = "3.0.0" }))
	a = admin{in: a.in.Reboot(), s: a.s}
	info = listed(t, a, "demo")
	if info["installed"].(map[string]any)["version"] != "2.0.0" || info["source"] != "upload" {
		t.Fatalf("a deployment overwrote a package the operator installed: %v", info)
	}
}

func TestChangingPluginsWhileTrafficFlowsRacesWithNothing(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{"demo.secret": "tok"})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int32
	var first atomic.Pointer[string]
	note := func(res *httptest.ResponseRecorder, what string) {
		bad.Add(1)
		msg := what + ": " + strconv.Itoa(res.Code) + " " + res.Body.String()
		first.CompareAndSwap(nil, &msg)
	}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				n++
				var res *httptest.ResponseRecorder
				switch i % 3 {
				case 0:
					res = a.in.Do(http.MethodGet, "/api/site", nil, nil)
				case 1:
					res = a.in.DoWith(http.MethodPost, "/api/x/demo/hook", map[string]string{"name": "n"}, nil,
						http.Header{"Authorization": {"Bearer tok"}})
					if res.Code != http.StatusOK && res.Code != http.StatusNotFound {
						note(res, "hook")
					}
					continue
				default:
					res = a.in.Do(http.MethodPost, "/api/auth/register",
						map[string]any{"username": "r" + strconv.Itoa(i) + "x" + strconv.Itoa(n), "password": founderPassword}, nil)
					if res.Code != http.StatusCreated && res.Code != http.StatusForbidden && res.Code != http.StatusTooManyRequests {
						note(res, "register")
					}
					continue
				}
				if res.Code != http.StatusOK {
					note(res, "site")
				}
			}
		}(i)
	}

	newer := repack(t, pkgtest.Demo(t), func(m map[string]any) { m["version"] = "1.5.0" })
	for round := 0; round < 4; round++ {
		a.mustDo(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{}, http.StatusOK)
		a.mustDo(http.MethodPost, "/api/admin/plugins/demo/enable", nil, http.StatusOK)
		a.setSettings(map[string]string{"demo.mode": []string{"open", "closed"}[round%2]})
	}
	a.in.InstallPackage(a.s, newer, true, nil)
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/uninstall", map[string]any{"purge": false}, http.StatusOK)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	close(stop)
	wg.Wait()
	if n := bad.Load(); n != 0 {
		t.Fatalf("%d requests answered something unexpected while plugins were changing; the first: %s", n, *first.Load())
	}
	a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
}

func TestABackendEndsAccountsWithTheOperationsTheServerOffers(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	victim := a.in.Register("victim", founderPassword)
	a.mustDo(http.MethodPost, "/api/admin/users/"+victim.UserID+"/cards", map[string]any{"cards": 3, "card_days": 30}, http.StatusCreated)

	// The last administrator is the core's rule to state, and it is refused.
	res := a.do(http.MethodPost, "/api/admin/x/demo/retire/"+a.s.UserID, map[string]any{"mode": "disable"})
	if res.Code != http.StatusConflict || code(t, res) != "last_admin" {
		t.Fatalf("retiring the last administrator: %d %s", res.Code, res.Body.String())
	}

	res = a.mustDo(http.MethodPost, "/api/admin/x/demo/retire/"+victim.UserID, map[string]any{"mode": "disable", "cards": 2}, http.StatusOK)
	if !strings.Contains(res.Body.String(), `"revoked":2`) {
		t.Fatalf("cards taken back: %s", res.Body.String())
	}
	if res := a.in.Do(http.MethodGet, "/api/auth/me", nil, victim); res.Code != http.StatusUnauthorized {
		t.Fatalf("a retired account's session: %d", res.Code)
	}
	var status string
	if err := a.in.DB.QueryRow(t.Context(), `SELECT status FROM users WHERE id = ?`, victim.UserID).Scan(&status); err != nil || status != "disabled" {
		t.Fatalf("status = %q %v", status, err)
	}
	var cards int
	_ = a.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM usage_cards WHERE user_id = ?`, victim.UserID).Scan(&cards)
	if cards != 1 {
		t.Fatalf("%d cards left after taking back two of three", cards)
	}

	a.mustDo(http.MethodPost, "/api/admin/x/demo/retire/"+victim.UserID, map[string]any{"mode": "delete"}, http.StatusOK)
	var n int
	_ = a.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM users WHERE id = ?`, victim.UserID).Scan(&n)
	if n != 0 {
		t.Fatal("the account survived a delete")
	}

	// Without the permission the same call is refused, and nothing changes.
	weak := newAdmin(t)
	weak.in.InstallPackage(weak.s, repack(t, pkgtest.Demo(t), func(m map[string]any) { m["permissions"] = []string{"db", "sessions"} }), true, nil)
	other := weak.in.Register("other", founderPassword)
	if res := weak.do(http.MethodPost, "/api/admin/x/demo/retire/"+other.UserID, map[string]any{"mode": "delete"}); res.Code != http.StatusInternalServerError {
		t.Fatalf("a retire without the users permission: %d %s", res.Code, res.Body.String())
	}
	if res := weak.in.Do(http.MethodGet, "/api/auth/me", nil, other); res.Code != http.StatusOK {
		t.Fatalf("the account was touched by a call that was refused: %d", res.Code)
	}
}

// A restore reads the bundle for the migrations of plugins the backup names
// but does not carry. The bundle may hold files that are not packages, and a
// package that will not parse must not stop the ones beside it being offered.
func TestBundledMigrationsOffersEveryValidPackageInTheBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.arcx"), pkgtest.Demo(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.arcx"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources := plugin.BundledMigrations(dir)
	if len(sources) != 1 {
		t.Fatalf("BundledMigrations offered %d sources; want the demo's alone", len(sources))
	}
	if body, err := fs.ReadFile(sources[0], "demo_0001_things.sql"); err != nil || !strings.Contains(string(body), "demo_things") {
		t.Errorf("demo's first migration was not offered: %v", err)
	}
	if got := plugin.BundledMigrations(filepath.Join(dir, "missing")); len(got) != 0 {
		t.Errorf("a bundle directory that does not exist offered %d sources", len(got))
	}
	if got := plugin.BundledMigrations(""); len(got) != 0 {
		t.Errorf("no bundle directory configured offered %d sources", len(got))
	}
}

// The runtime's defaults are fakes — a clock that reads 2022-01-01 and a
// deterministic random source — and a plugin could not tell: its rows would be
// stamped with the wrong year and its tokens would repeat. Checked through a
// whole server, where the row is written.
// A package that lists the sweep hook is given a turn by the server's
// periodic cleanup while it is switched on, and none while it is off.
func TestASweepingPackageGetsItsTurnOnlyWhileSwitchedOn(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	swept := func() int {
		var n int
		if err := a.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM demo_things WHERE name = 'swept'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	a.in.Server.SweepPlugins(t.Context())
	a.in.Server.SweepPlugins(t.Context())
	if n := swept(); n != 2 {
		t.Fatalf("%d turns from two sweeps", n)
	}
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{}, http.StatusOK)
	a.in.Server.SweepPlugins(t.Context())
	if n := swept(); n != 2 {
		t.Fatal("a switched-off package was given a turn")
	}
}

func TestABackendStampsRowsWithTheRealTime(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	before := time.Now().UnixMilli()
	a.mustDo(http.MethodPost, "/api/admin/x/demo/things", map[string]string{"name": "clock"}, http.StatusCreated)
	after := time.Now().UnixMilli()
	var created int64
	if err := a.in.DB.QueryRow(t.Context(), `SELECT created_at FROM demo_things WHERE name = 'clock'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created < before-1000 || created > after+1000 {
		t.Fatalf("the backend stamped %s; the server's clock says %s", time.UnixMilli(created).UTC(), time.UnixMilli(before).UTC())
	}
}

// A backend's own error reaches the client whatever its status — a 503 because
// the service it stands in front of is down is its answer as much as a 404 is —
// while a failure nobody worded, whose text may be a database password, is the
// server's own 500 and the text goes to the log.
func TestABackendsChosenErrorsAreShownAndUnchosenOnesAreHidden(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)

	res := a.do(http.MethodPost, "/api/admin/x/demo/unavailable", nil)
	if res.Code != http.StatusServiceUnavailable || code(t, res) != "demo_unavailable" ||
		!strings.Contains(res.Body.String(), "The demo service is down.") {
		t.Fatalf("a chosen 503: %d %s", res.Code, res.Body.String())
	}
	res = a.do(http.MethodPost, "/api/admin/x/demo/explode", nil)
	if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), "hunter2") {
		t.Fatalf("an unchosen failure: %d %s", res.Code, res.Body.String())
	}

	register := func(token string) *httptest.ResponseRecorder {
		return a.in.Do(http.MethodPost, "/api/auth/register", map[string]any{
			"username": "guest-" + token, "password": founderPassword, "guards": map[string]string{"demo": token},
		}, nil)
	}
	res = register("down")
	if res.Code != http.StatusServiceUnavailable || code(t, res) != "demo_unavailable" {
		t.Fatalf("a guard's own 503: %d %s", res.Code, res.Body.String())
	}
	res = register("explode")
	if res.Code != http.StatusServiceUnavailable || code(t, res) != "plugin_unavailable" || strings.Contains(res.Body.String(), "hunter2") {
		t.Fatalf("a guard that failed unchosen must refuse without saying why: %d %s", res.Code, res.Body.String())
	}
}

// The Host the client addressed is what tells a backend whether a request came
// in on this instance's own domain.
func TestABackendIsToldTheHostTheClientAddressed(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	res := a.in.Do(http.MethodGet, "http://ai.example.test/api/x/demo/echo", nil, nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"host":"ai.example.test"`) {
		t.Fatalf("echo: %d %s", res.Code, res.Body.String())
	}
}

// A command that lets a refused Call escape is drawn the way a compiled-in
// command's failure is: the endpoint's sentence with its code beside it.
func TestACommandThatPassesOnARefusedCallIsDrawnWithTheEndpointsCode(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)

	out, ok := consoleLine(t, a, "demo add")
	plain := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(out, "")
	if ok || !strings.Contains(plain, "error: A name is required. (name_required)") {
		t.Fatalf("a refused call: ok=%v %q", ok, out)
	}
	if out, ok := consoleLine(t, a, "demo add gadget"); !ok || !strings.Contains(out, "added gadget") {
		t.Fatalf("an accepted call: ok=%v %q", ok, out)
	}
}

func TestABackendPaysRewardsInTheCoresCurrenciesAndOnlyWithThePermission(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	winner := a.in.Register("winner", founderPassword)
	bar := servertest.Decode[struct {
		Bar struct {
			ID string `json:"id"`
		} `json:"bar"`
	}](t, a.mustDo(http.MethodPost, "/api/admin/bonus/bars", map[string]any{"name": "Prizes"}, http.StatusCreated)).Bar

	res := a.mustDo(http.MethodPost, "/api/admin/x/demo/reward/"+winner.UserID,
		map[string]any{"bar_id": bar.ID, "amount": 2.5, "cards": 2, "days": 3}, http.StatusOK)
	expires := servertest.Decode[struct {
		ExpiresAt int64 `json:"expires_at"`
	}](t, res).ExpiresAt
	if lifetime := time.Until(time.UnixMilli(expires)); lifetime < 71*time.Hour || lifetime > 73*time.Hour {
		t.Fatalf("a three-day bonus expires in %v", lifetime)
	}
	var amount float64
	var source string
	if err := a.in.DB.QueryRow(t.Context(), `SELECT amount, source FROM bonus_grants WHERE user_id = ?`, winner.UserID).Scan(&amount, &source); err != nil {
		t.Fatal(err)
	}
	// Marked as the plugin's, so the bonus page can say where it came from.
	if amount != 2.5 || source != "plugin:demo" {
		t.Fatalf("grant = %v from %q", amount, source)
	}
	count := func(query string) int {
		var n int
		if err := a.in.DB.QueryRow(t.Context(), query, winner.UserID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM usage_cards WHERE user_id = ? AND name = 'demo'`); n != 2 {
		t.Fatalf("%d cards granted, want 2", n)
	}

	// Inside the backend's transaction: a reward whose reason is taken back
	// is taken back with it.
	if res := a.do(http.MethodPost, "/api/admin/x/demo/reward/"+winner.UserID,
		map[string]any{"bar_id": bar.ID, "amount": 1, "cards": 1, "fail": true}); res.Code != http.StatusConflict {
		t.Fatalf("a failed reward: %d %s", res.Code, res.Body.String())
	}
	if count(`SELECT COUNT(*) FROM bonus_grants WHERE user_id = ?`) != 1 || count(`SELECT COUNT(*) FROM usage_cards WHERE user_id = ?`) != 2 {
		t.Fatal("a rolled-back reward was paid anyway")
	}
	if res := a.do(http.MethodPost, "/api/admin/x/demo/reward/"+winner.UserID,
		map[string]any{"bar_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "amount": 1}); res.Code != http.StatusNotFound || code(t, res) != "demo_no_bar" {
		t.Fatalf("a bar that does not exist: %d %s", res.Code, res.Body.String())
	}

	weak := newAdmin(t)
	weak.in.InstallPackage(weak.s, repack(t, pkgtest.Demo(t), func(m map[string]any) { m["permissions"] = []string{"db", "cards"} }), true, nil)
	other := weak.in.Register("other", founderPassword)
	if res := weak.do(http.MethodPost, "/api/admin/x/demo/reward/"+other.UserID, map[string]any{"cards": 1}); res.Code != http.StatusInternalServerError {
		t.Fatalf("a reward without the rewards permission: %d %s", res.Code, res.Body.String())
	}
	var n int
	_ = weak.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM usage_cards WHERE user_id = ?`, other.UserID).Scan(&n)
	if n != 0 {
		t.Fatal("cards were granted by a call that was refused")
	}
}

func TestABackendBorrowsTheInstancesHumanCheckAsTheSignUpDoorAsksForIt(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	reader := a.in.Register("reader", founderPassword)
	a.setSettings(map[string]string{"registration.captcha_mode": "pow", "security.pow_base_max_number": "200"})

	if res := a.in.Do(http.MethodPost, "/api/x/demo/challenged", map[string]any{}, reader); res.Code != http.StatusBadRequest || code(t, res) != "challenge_failed" {
		t.Fatalf("no proof: %d %s", res.Code, res.Body.String())
	}
	solve := func() map[string]any {
		challenge := servertest.Decode[map[string]any](t, a.in.Do(http.MethodGet, "/api/auth/pow-challenge", nil, reader))
		salt, _ := challenge["salt"].(string)
		maxNumber := int64(challenge["maxNumber"].(float64))
		for n := int64(0); n <= maxNumber; n++ {
			sum := sha256.Sum256([]byte(salt + strconv.FormatInt(n, 10)))
			if hex.EncodeToString(sum[:]) == challenge["challenge"] {
				challenge["nonce"] = n
				return challenge
			}
		}
		t.Fatal("the challenge has no solution")
		return nil
	}
	solved := solve()
	res := a.in.Do(http.MethodPost, "/api/x/demo/challenged", map[string]any{"pow": solved}, reader)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"pow":true`) {
		t.Fatalf("a solved proof: %d %s", res.Code, res.Body.String())
	}
	// Spent once, like the sign-up door's own.
	if res := a.in.Do(http.MethodPost, "/api/x/demo/challenged", map[string]any{"pow": solved}, reader); res.Code != http.StatusBadRequest {
		t.Fatalf("a replayed proof: %d %s", res.Code, res.Body.String())
	}

	// A sign-up mode the core does not draw still means a check: proof of
	// work, when the page loads no Turnstile.
	a.setSettings(map[string]string{"registration.captcha_mode": "off"})
	if res := a.in.Do(http.MethodPost, "/api/x/demo/challenged", map[string]any{}, reader); res.Code != http.StatusBadRequest {
		t.Fatalf("no proof with the sign-up check off: %d %s", res.Code, res.Body.String())
	}

	weak := newAdmin(t)
	weak.in.InstallPackage(weak.s, repack(t, pkgtest.Demo(t), func(m map[string]any) { m["permissions"] = []string{"db"} }), true, nil)
	if res := weak.in.Do(http.MethodPost, "/api/x/demo/challenged", map[string]any{}, weak.s); res.Code != http.StatusInternalServerError {
		t.Fatalf("a check without the challenge permission: %d %s", res.Code, res.Body.String())
	}
}
