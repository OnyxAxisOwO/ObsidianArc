package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// packageOf builds a package with no backend: a manifest, and the migration
// files given (name → SQL).
func packageOf(t *testing.T, name string, migrations map[string]string, edit ...func(map[string]any)) []byte {
	t.Helper()
	manifest := map[string]any{
		"arcx": 1, "name": name, "version": "1.0.0", "title": map[string]string{"en": name},
		"requires": map[string]int{"api": 1},
	}
	for _, apply := range edit {
		apply(manifest)
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	put := func(file, content string) {
		w, err := zw.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	put("manifest.json", string(body))
	for file, sql := range migrations {
		put("migrations/"+file, sql)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func parsed(t *testing.T, raw []byte) *arcx.Package {
	t.Helper()
	pkg, err := arcx.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func tableSQL(table string) string {
	return "CREATE TABLE " + table + " (id TEXT PRIMARY KEY);"
}

// Every owner's migrations are recorded in one table under their file names,
// and a version already recorded is skipped. A package that shares one with
// the core, a compiled-in plugin or another package would be silently not
// run, then fail the boot, then take the other owner's record with it when
// it is removed with its data.
func TestAPackageMigrationCannotTakeAVersionSomebodyElseHolds(t *testing.T) {
	r := newRig(t, fake{name: "legacyp", setup: noSetup, dir: fstest.MapFS{
		"0020_legacy_plugin.sql": {Data: []byte(tableSQL("legacy_plugin_table"))},
	}})
	ctx := context.Background()

	refused := func(label string, raw []byte) {
		t.Helper()
		if _, err := r.manager.Preview(someone, raw); !errors.Is(err, ErrPreflight) {
			t.Errorf("%s: %v", label, err)
		}
	}
	core, err := database.CoreVersions()
	if err != nil || len(core) == 0 {
		t.Fatal(core, err)
	}
	newest := versionNumber(core[len(core)-1])

	refused("a version the core ships",
		packageOf(t, "alpha", map[string]string{"0016_provider_insecure_http.sql": tableSQL("alpha_one")}))
	refused("a version a compiled-in plugin ships",
		packageOf(t, "alpha", map[string]string{"0020_legacy_plugin.sql": tableSQL("alpha_two")}))
	refused("a numbered name the core has not reached yet",
		packageOf(t, "alpha", map[string]string{fmt.Sprintf("%04d_ahead.sql", newest+1): tableSQL("alpha_three")}))
	refused("a numbered name at the core's newest",
		packageOf(t, "alpha", map[string]string{fmt.Sprintf("%04d_ahead.sql", newest): tableSQL("alpha_four")}))

	// A migration that moved out of the core keeps its number, and that is
	// allowed where the core has gone by it; so is the package's own prefix.
	moved := fmt.Sprintf("%04d_moved_alpha.sql", newest-1)
	if _, err := r.manager.Preview(someone, packageOf(t, "alpha", map[string]string{
		moved: tableSQL("alpha_moved"), "alpha_0001_things.sql": tableSQL("alpha_things"),
	})); err != nil {
		t.Fatalf("a moved migration and a prefixed one: %v", err)
	}

	// Another package's, once it is installed — but not the package's own on
	// an update.
	if _, err := r.manager.InstallPackage(ctx, someone, parsed(t, packageOf(t, "alpha", map[string]string{moved: tableSQL("alpha_moved")})),
		SourceUpload, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	refused("a version another package holds",
		packageOf(t, "beta", map[string]string{moved: tableSQL("beta_moved")}))
	if _, err := r.manager.Preview(someone, packageOf(t, "alpha", map[string]string{moved: tableSQL("alpha_moved")},
		func(m map[string]any) { m["version"] = "1.1.0" })); err != nil {
		t.Fatalf("an update that keeps its own migration: %v", err)
	}
}

// What an earlier build let in must not be able to stop the server starting:
// the migration run refuses two owners of one version, so the package that
// would cause it is left out and the rest carry on.
func TestAStoredPackageWhoseMigrationsCollideIsLeftOutAndTheBootCarriesOn(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.manager.InstallPackage(ctx, someone, parsed(t, packageOf(t, "healthy", map[string]string{
		"healthy_0001_things.sql": tableSQL("healthy_things"),
	})), SourceUpload, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	store := func(name, file string) {
		t.Helper()
		raw := packageOf(t, name, map[string]string{file: tableSQL(name + "_table")})
		if _, err := r.db.Exec(ctx,
			`INSERT INTO plugin_packages (name, version, sha256, source, archive, added_at, added_by)
			 VALUES (?, '1.0.0', ?, 'upload', ?, 1, '')`, name, strings.Repeat("0", 64), raw); err != nil {
			t.Fatal(err)
		}
	}
	store("clashcore", "0016_provider_insecure_http.sql")
	store("clashone", "0021_shared.sql")
	store("clashtwo", "0021_shared.sql")

	again := reload(t, r.db)
	if again.manager.loadedPackage("healthy") == nil {
		t.Fatal("a healthy package was dropped along with the ones that collide")
	}
	if again.manager.loadedPackage("clashcore") != nil {
		t.Error("a package that took a core version was loaded")
	}
	one, two := again.manager.loadedPackage("clashone") != nil, again.manager.loadedPackage("clashtwo") != nil
	if one == two {
		t.Errorf("of two packages holding one version, loaded: first=%v second=%v", one, two)
	}
}

func accountOf(t *testing.T, r *rig, name string, role user.Role) user.User {
	t.Helper()
	account, err := r.users.Create(context.Background(), nil, user.CreateInput{Username: name, PasswordHash: "hash", Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

// userCall is a package's backend asking the host to end an account, inside
// the transaction tx when there is one.
func (r *rig) userCall(op, id string, tx *database.Tx) error {
	raw := fmt.Sprintf(`{"id":%q,"status":"disabled","tx":%t}`, id, tx != nil)
	_, err := r.manager.userOp(&wasm.Call{Ctx: context.Background()}, &callState{tx: tx}, op, json.RawMessage(raw))
	return err
}

func hostCode(err error) string {
	var he *wasm.HostError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

func (r *rig) statusOf(t *testing.T, id string) (status string, exists bool) {
	t.Helper()
	err := r.db.QueryRow(context.Background(), `SELECT status FROM users WHERE id = ?`, id).Scan(&status)
	if database.IsNotFound(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return status, true
}

// A package is not part of the administrators' hierarchy, and the last way
// into the instance is the core's to protect.
func TestAPackageCannotSuspendOrDeleteAnAdministrator(t *testing.T) {
	r := newRig(t)
	only := accountOf(t, r, "only", user.RoleSuperAdmin)
	second := accountOf(t, r, "second", user.RoleSuperAdmin)
	staff := accountOf(t, r, "staff", user.RoleAdmin)
	member := accountOf(t, r, "member", user.RoleUser)

	for _, op := range []string{"users.set_status", "users.delete"} {
		for label, target := range map[string]user.User{"a super administrator": only, "another super administrator": second, "an administrator": staff} {
			err := r.userCall(op, target.ID, nil)
			if code := hostCode(err); code != "admin_account" && code != "last_admin" {
				t.Errorf("%s on %s: %v", op, label, err)
			}
			if status, exists := r.statusOf(t, target.ID); !exists || status != "active" {
				t.Errorf("%s on %s changed it: exists=%v status=%q", op, label, exists, status)
			}
		}
	}

	// Alone at the top, the refusal is the core's own sentence for it.
	if _, err := r.db.Exec(context.Background(), `UPDATE users SET status = 'disabled' WHERE id = ?`, second.ID); err != nil {
		t.Fatal(err)
	}
	if code := hostCode(r.userCall("users.set_status", only.ID, nil)); code != "last_admin" {
		t.Errorf("the last active super administrator: %q", code)
	}

	// An ordinary account is what a package is for, and an account already gone
	// is nothing to delete.
	if err := r.userCall("users.set_status", member.ID, nil); err != nil {
		t.Fatal(err)
	}
	if status, _ := r.statusOf(t, member.ID); status != "disabled" {
		t.Errorf("status = %q", status)
	}
	if err := r.userCall("users.delete", member.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := r.statusOf(t, member.ID); exists {
		t.Error("the account survived a delete")
	}
	if err := r.userCall("users.delete", member.ID, nil); err != nil {
		t.Errorf("deleting an account that is already gone: %v", err)
	}
}

// Inside the backend's own transaction the change is the transaction's: ended
// with it, not committed on the side.
func TestAnAccountChangeInABackendsTransactionIsPartOfIt(t *testing.T) {
	r := newRig(t)
	member := accountOf(t, r, "member", user.RoleUser)
	tx, err := r.db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.userCall("users.set_status", member.ID, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if status, _ := r.statusOf(t, member.ID); status != "active" {
		t.Fatalf("a rolled-back change stayed: %q", status)
	}
}

// The backoffice promotes and demotes under a lock on the administrators; an
// account a package is about to end may be being made one at that moment. The
// package's change waits for that, and then sees an administrator.
func TestAnAccountChangeWaitsForWhoeverIsChangingTheAdministrators(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	target := accountOf(t, r, "target", user.RoleUser)

	promotion, err := r.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.Lock(ctx, promotion); err != nil {
		t.Fatal(err)
	}
	if _, err := promotion.Exec(ctx, `UPDATE users SET role = ? WHERE id = ?`, user.RoleAdmin, target.ID); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- r.userCall("users.delete", target.ID, nil) }()
	select {
	case err := <-done:
		t.Fatalf("the delete finished while the administrators were being changed: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := promotion.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; hostCode(err) != "admin_account" {
		t.Fatalf("the delete after the promotion: %v", err)
	}
	if _, exists := r.statusOf(t, target.ID); !exists {
		t.Fatal("an account promoted to administrator was deleted by a package")
	}
}

// A panic in a check must fail that request. If it left the manager's lock
// held, every later install, switch and removal would wait for it forever.
func TestAPanicInAPackageCheckDoesNotLeaveThePluginManagerLocked(t *testing.T) {
	r := newRig(t)
	// A manifest as ParseManifest would never let through.
	broken := &arcx.Package{Manifest: &arcx.Manifest{
		Name: "alpha", Version: "1", Settings: []arcx.Setting{{Key: "alpha.x", Pattern: `\Qabc`}},
	}}
	func() {
		defer func() { _ = recover() }()
		_, _ = r.manager.checkAgainstInstalled(broken)
	}()
	if !r.manager.mu.TryLock() {
		t.Fatal("the plugin manager is still locked")
	}
	r.manager.mu.Unlock()
}

func TestOriginsAPackageAsksForAreOriginsAndAFewAtMost(t *testing.T) {
	got := cleanOrigins("p", []string{
		"https://a.example.com", "*", "https:", "'unsafe-eval'", "blob:", "https://b.example.com/x",
		"http://c.example.org:81", "https://*.example.com", "data:",
	})
	if want := "https://a.example.com blob: http://c.example.org:81"; strings.Join(got, " ") != want {
		t.Errorf("kept %q, want %q", strings.Join(got, " "), want)
	}
	many := make([]string, 20)
	for i := range many {
		many[i] = fmt.Sprintf("https://h%d.example.com", i)
	}
	if got := cleanOrigins("p", many); len(got) != maxOrigins {
		t.Errorf("kept %d of 20 origins, want %d", len(got), maxOrigins)
	}
}

func TestPlainTextKeepsLayoutAndDropsWhatATerminalObeys(t *testing.T) {
	in := "a\x1b[31mb\x07\r\n\tc\u0085d\x00e"
	if got, want := plainText(in), "a[31mb\n\tcde"; got != want {
		t.Errorf("plainText(%q) = %q, want %q", in, got, want)
	}
	if got := plainText("名前 ✓"); got != "名前 ✓" {
		t.Errorf("printable text changed: %q", got)
	}
}

// A flood at a public route or a sign-up form must not make as many instances
// as it has requests. The call past the bound is turned away, and a guard that
// is turned away refuses: a check that did not run is not a check that passed.
func TestAGuardTheEngineHasNoRoomForRefusesAndARouteSaysBusy(t *testing.T) {
	wasm.ShareCompiledCode()
	held := make(chan struct{})
	arrived := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		<-held
	}))
	defer upstream.Close()
	defer close(held)

	r := newRig(t)
	r.manager.engOnce.Do(func() { r.manager.eng = wasm.NewEngine(wasm.Limits{MaxConcurrent: 1}) })
	ctx := context.Background()
	if _, err := r.manager.InstallPackage(ctx, someone, parsed(t, pkgtest.Demo(t)), SourceUpload, InstallOptions{
		Enable: true, Settings: map[string]string{"demo.upstream": upstream.URL},
	}); err != nil {
		t.Fatal(err)
	}
	l := r.manager.loadedPackage("demo")

	// A request that holds the engine's one slot for as long as its upstream
	// takes to answer.
	go func() {
		var out map[string]any
		_ = r.manager.invoke(ctx, l, wasm.CallInfo{}, "http", map[string]any{
			"route": "GET /api/x/demo/upstream", "method": "GET", "host": "x", "path": "/api/x/demo/upstream",
			"header": map[string]string{}, "params": map[string]string{}, "body": "",
		}, &out, nil)
	}()
	select {
	case <-arrived:
	case <-time.After(30 * time.Second):
		t.Fatal("the backend never reached its upstream")
	}

	_, err := r.manager.judge(ctx, l, "demo", auth.GuardRequest{Action: auth.GuardLogin, IP: "203.0.113.9", Username: "someone"})
	var refusal *auth.GuardRefusal
	var answer *httpx.Error
	if !errors.As(err, &refusal) || !errors.As(err, &answer) || answer.Status != http.StatusServiceUnavailable {
		t.Fatalf("a guard with no room to run: %v", err)
	}

	err = translateGuest(wasm.ErrBusy)
	if !errors.As(err, &answer) || answer.Status != http.StatusServiceUnavailable || answer.Code != "plugin_busy" {
		t.Fatalf("a route with no room to run answers %v", err)
	}
}
