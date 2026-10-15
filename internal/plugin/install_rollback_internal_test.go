package plugin

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
)

// A host with the seams a package attaches to, and nothing behind them: the
// services are zero values, which the extension points accept.
func rigHost() *Host {
	return &Host{OAuth: &oauth.Service{}, Auth: &auth.Service{}, AuthHandlers: &auth.Handlers{}, InviteHandlers: &invite.Handlers{}}
}

func bindingOn(field string) arcx.OAuthBinding {
	return arcx.OAuthBinding{Provider: "oidc", Field: field, Pattern: "^[0-9]+$"}
}

// A failed update puts the old version back, and must take back what only the
// new one brought: its sign-in binding and its field rule. The binding is the
// one that breaks the rollback if it stays: the old version's binding cannot be
// bound again while the new one holds the sign-in's only slot.
func TestARolledBackUpdateTakesBackWhatOnlyTheNewVersionBrought(t *testing.T) {
	r := newRig(t)
	m := r.manager
	h := rigHost()
	m.host = h
	r.settings.Remember(map[string]string{gadgetKey: "required"})

	old := &loaded{name: "gadget", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.0.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("student_id")},
	}}}
	next := &loaded{name: "gadget", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.1.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("phone")},
		FieldRules: []arcx.FieldRule{{Field: "handle", Setting: gadgetKey}},
	}}}

	m.swapPackages(func(p map[string]*loaded) { p["gadget"] = old })
	if err := m.attach(old); err != nil {
		t.Fatal(err)
	}
	// The update's transaction: retire what it dropped, attach what it brought,
	// then fail, which is what the rollback is for.
	m.dropRetiredSeams(old.pkg.Manifest, next.pkg.Manifest)
	m.swapPackages(func(p map[string]*loaded) { p["gadget"] = next })
	if err := m.attach(next); err != nil {
		t.Fatal(err)
	}
	if h.Auth.FieldRule("handle") != auth.FieldRequired {
		t.Fatal("the new version's field rule did not come live, so the test proves nothing")
	}

	m.restoreInstall("gadget", old, next)

	if field := h.OAuth.SubjectBindingField(); field != "student_id" {
		t.Fatalf("after the rollback the sign-in is bound to %q, not the old version's student_id", field)
	}
	if got := h.Auth.FieldRule("handle"); got == auth.FieldRequired {
		t.Fatal("the new version's field rule survived the rollback")
	}
}

// An update may move its own sign-in binding to another field: the install
// retires the old one first. Another package's binding still refuses.
func TestAnUpdateMayMoveItsOwnSubjectBindingButAnotherOwnersIsStillRefused(t *testing.T) {
	r := newRig(t)
	m := r.manager
	h := rigHost()
	m.host = h
	old := &loaded{name: "gadget", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.0.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("email")},
	}}}
	if err := m.attach(old); err != nil {
		t.Fatal(err)
	}

	moved := &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.1.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("phone")},
	}}
	if err := m.checkPackage(moved, old); err != nil {
		t.Fatalf("an update that moves its own binding is refused: %v", err)
	}

	rival := &arcx.Package{Manifest: &arcx.Manifest{
		Name: "rival", Version: "1.0.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("phone")},
	}}
	if err := m.checkPackage(rival, nil); !errors.Is(err, ErrPreflight) {
		t.Fatalf("a package that does not hold the binding was let through: %v", err)
	}
}

// A sign-in has one subject binding, so a package asking for two could never be
// installed. It is refused as a preflight answer, not an internal error from
// inside the transaction, whatever the host already has bound.
func TestAPackageWithTwoSubjectBindingsIsRefusedBeforeAnythingChanges(t *testing.T) {
	r := newRig(t)
	r.manager.host = rigHost()
	pkg := &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.0.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("email"), bindingOn("phone")},
	}}
	if err := r.manager.checkPackage(pkg, nil); !errors.Is(err, ErrPreflight) {
		t.Fatalf("two bindings were let through: %v", err)
	}
}

// Packages that conflict at boot fault the same one every boot. The one attached
// first is the one whose route table rebuild fails, so the order must not be the
// map's.
func TestWhichOfTwoConflictingBootPackagesIsFaultedIsTheSameEveryBoot(t *testing.T) {
	for range 20 {
		r := newRig(t)
		m := r.manager
		clash := func(name string) *loaded {
			return &loaded{name: name, pkg: &arcx.Package{Manifest: &arcx.Manifest{
				Name: name, Version: "1.0.0", Routes: []arcx.Route{{Pattern: "GET /api/clash", Access: "public"}},
			}}}
		}
		m.pkgs.Store(&map[string]*loaded{"alpha": clash("alpha"), "bravo": clash("bravo")})
		h := rigHost()
		h.Mux = http.NewServeMux()
		m.Attach(h)
		faulted := map[string]bool{}
		for name, l := range *m.pkgs.Load() {
			faulted[name] = l.faulted() != ""
		}
		if !faulted["alpha"] || faulted["bravo"] {
			t.Fatalf("faulted = %v; the package attached first should be the one faulted, every time", faulted)
		}
	}
}

// Parked uploads are held by size as well as by count: eight archives of the
// largest size allowed would be a quarter of a gigabyte.
func TestPendingUploadsAreBoundedInBytesNotOnlyInCount(t *testing.T) {
	var p pendingUploads
	first := p.put("alice", &arcx.Package{Raw: make([]byte, 30<<20)})
	for range 2 {
		p.put("alice", &arcx.Package{Raw: make([]byte, 30<<20)})
	}
	p.mu.Lock()
	_, held := p.items[first]
	total := 0
	for _, item := range p.items {
		total += len(item.raw)
	}
	p.mu.Unlock()
	if held {
		t.Fatal("the oldest upload was kept although the newer ones pushed the total over the budget")
	}
	if total > pendingBudget {
		t.Fatalf("%d bytes held, over the %d budget", total, pendingBudget)
	}
}

// A package whose archive stopped parsing keeps its row. It is off, not on with
// nothing behind it, and the operator can still switch it off and remove it.
func TestAnUnreadableInstalledPackageCanStillBeSwitchedOffAndRemoved(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.manager.InstallPackage(ctx, someone, parsed(t, packageOf(t, "orphan", nil)), SourceUpload,
		InstallOptions{Enable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(ctx, `UPDATE plugin_packages SET archive = ? WHERE name = ?`, []byte("not a zip"), "orphan"); err != nil {
		t.Fatal(err)
	}

	next := reload(t, r.db)
	m := next.manager
	if m.Enabled("orphan") {
		t.Fatal("an unreadable package is switched on with nothing loaded behind it")
	}
	if err := m.Disable(ctx, someone, "orphan"); err != nil {
		t.Fatalf("switching it off: %v", err)
	}
	if err := m.Uninstall(ctx, someone, "orphan", false); err != nil {
		t.Fatalf("removing it: %v", err)
	}
	var rows int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM plugin_packages WHERE name = ?`, "orphan").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("its archive is still stored after it was removed")
	}
}

// plugin() stands in for an installed package only when its archive could not be
// read at boot, which is the boot-time set and nothing else. A row in any other
// state is not that: a plugin this build does not carry has a row and nothing
// behind it, and has no stand-in to switch off.
func TestARowForAPluginThisBuildDoesNotCarryHasNoStandIn(t *testing.T) {
	r := newRig(t)
	if _, err := r.db.Exec(context.Background(),
		`INSERT INTO plugin_installs (name, state, version, installed_at, installed_by, updated_at, updated_by)
		 VALUES (?, ?, '', 1, '', 1, '')`, "ghost", string(StateDisabled)); err != nil {
		t.Fatal(err)
	}
	next := reload(t, r.db)
	if _, ok := next.manager.plugin("ghost"); ok {
		t.Fatal("a row for a plugin this build does not carry was given a stand-in")
	}
}

// Settings saves start describe refreshes in the background. Requests that
// arrive while one runs fold into a single further run, which starts after it,
// so the last change is read and nothing runs twice at once.
func TestSettingsSavesDuringARefreshFoldIntoOneMoreRun(t *testing.T) {
	var q describeQueue
	var runs, inside atomic.Int32
	var overlap atomic.Bool
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	refresh := func() {
		if inside.Add(1) > 1 {
			overlap.Store(true)
		}
		if runs.Add(1) == 1 {
			once.Do(func() { close(started) })
			<-release
		}
		inside.Add(-1)
	}

	q.request(refresh)
	<-started
	for range 5 {
		q.request(refresh)
	}
	close(release)

	deadline := time.Now().Add(10 * time.Second)
	for {
		q.mu.Lock()
		idle := !q.running
		q.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the queue never went idle")
		}
		time.Sleep(time.Millisecond)
	}
	if overlap.Load() {
		t.Fatal("two refreshes ran at once")
	}
	if n := runs.Load(); n != 2 {
		t.Fatalf("%d refreshes for one running and five saves during it; want 2", n)
	}
}

// When the previous version will not attach again, the guards of both versions
// are not left out: an absent check is a door open. They stay, and refuse.
func TestAFailedRollbackLeavesTheGuardsRefusing(t *testing.T) {
	r := newRig(t)
	m := r.manager
	h := rigHost()
	m.host = h
	h.Auth.SetPluginGate(plugingate.Gate(func(string) bool { return true }))

	// A package cannot define a core setting, so attaching this version fails.
	old := &loaded{name: "gadget", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.0.0", Settings: []arcx.Setting{{Key: "site.name"}},
	}}}
	next := &loaded{name: "gadget", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.1.0", Guards: []arcx.Guard{{Action: "register", Name: "check", Event: "gadget_check"}},
	}}}
	m.swapPackages(func(p map[string]*loaded) { p["gadget"] = next })
	if err := m.attach(next); err != nil {
		t.Fatal(err)
	}

	m.restoreInstall("gadget", old, next)

	if _, err := h.Auth.CheckSignupGuards(context.Background(), "", ""); err == nil {
		t.Fatal("sign-up was let through once the package's check was taken away by a failed rollback")
	}
}

// A save's channel closes when a refresh that reads its change has finished, not
// when the refresh already running has: the save returns with the description
// the new settings produced. Each refresh here waits for the test to let it end,
// so the order of the two closes is what is checked.
func TestASaveWaitsForTheRefreshThatReadsItNotTheOneAlreadyRunning(t *testing.T) {
	var q describeQueue
	var runs atomic.Int32
	gates := make(chan struct{})
	refresh := func() {
		runs.Add(1)
		<-gates
	}

	first := q.request(refresh)
	deadline := time.Now().Add(10 * time.Second)
	for runs.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first refresh never started")
		}
		time.Sleep(time.Millisecond)
	}
	// Only once the first run has begun is the second request one that the
	// first cannot read; before that, both would be covered by one run.
	second := q.request(refresh)
	select {
	case <-first:
		t.Fatal("a save returned before the refresh it started had finished")
	default:
	}

	gates <- struct{}{}
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("the first save never returned after its refresh")
	}
	select {
	case <-second:
		t.Fatal("a save returned before the refresh that reads it had run")
	default:
	}

	gates <- struct{}{}
	select {
	case <-second:
	case <-time.After(10 * time.Second):
		t.Fatal("the folded save never returned")
	}
	if n := runs.Load(); n != 2 {
		t.Fatalf("%d refreshes for two saves; want 2", n)
	}
}
