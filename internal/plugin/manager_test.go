package plugin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The settings registry is global and refuses a key defined twice, so the
// plugins these tests register share one set of keys, defined once.
var defineOnce sync.Once

const (
	gadgetKey  = "gadget.colour"
	gadgetOnly = "gadget.only_valid"
)

func defineGadget() {
	defineOnce.Do(func() {
		settings.Define(settings.Definition{Key: gadgetKey, Default: "blue", Plugin: "gadget"})
		settings.Define(settings.Definition{Key: gadgetOnly, Default: "yes", Plugin: "gadget",
			Validate: func(v string) error {
				if v != "yes" && v != "no" {
					return errors.New("yes or no")
				}
				return nil
			}})
	})
}

// gadget brings a table and knows how to take it away.
type gadget struct{ fake }

func (gadget) Purge() fs.FS {
	return fstest.MapFS{"0001_drop.sql": {Data: []byte("DROP TABLE IF EXISTS gadgets;")}}
}

func newGadget() gadget {
	return gadget{fake{name: "gadget", setup: noSetup, dir: fstest.MapFS{
		"gadget_0001_table.sql": {Data: []byte("CREATE TABLE gadgets (id TEXT PRIMARY KEY);")},
	}}}
}

type rig struct {
	db       *database.DB
	settings *settings.Service
	users    *user.Store
	manager  *Manager
}

func newRig(t *testing.T, plugins ...Plugin) *rig {
	t.Helper()
	isolate(t)
	defineGadget()
	for _, p := range plugins {
		Register(p)
	}
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "plugins.db"), MaxOpenConns: 8, MaxIdleConns: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return reload(t, db)
}

// reload is the next boot: a new manager over the same database.
func reload(t *testing.T, db *database.DB) *rig {
	t.Helper()
	ctx := context.Background()
	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	users := user.NewStore(db)
	m := NewManager(db, set, users, nil)
	if err := m.Load(ctx); err != nil {
		t.Fatal(err)
	}
	set.SetPluginGate(m.Gate())
	users.SetPluginGate(m.Gate())
	return &rig{db: db, settings: set, users: users, manager: m}
}

func (r *rig) tableExists(t *testing.T) bool {
	t.Helper()
	var n int
	if err := r.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'gadgets'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

var someone = Actor{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Username: "founder"}

// A fresh instance installs nothing by itself: a plugin compiled in is on
// offer, not on.
func TestACompiledInPluginStartsAvailable(t *testing.T) {
	r := newRig(t, newGadget())
	if state := r.manager.State("gadget"); state != StateAvailable {
		t.Fatalf("state = %s", state)
	}
	if r.tableExists(t) {
		t.Fatal("an uninstalled plugin's migration ran at boot")
	}
	if r.settings.Known(gadgetKey) {
		t.Fatal("an uninstalled plugin's setting is known")
	}
}

// Installing runs the migrations, writes the first settings and records the
// state, and the next boot finds it the same way.
func TestInstallRunsTheMigrationsAndKeepsTheSettings(t *testing.T) {
	r := newRig(t, newGadget())
	ctx := context.Background()
	if err := r.manager.Install(ctx, someone, "gadget", InstallOptions{
		Enable: true, Settings: map[string]string{gadgetKey: "green"},
	}); err != nil {
		t.Fatal(err)
	}
	if !r.manager.Enabled("gadget") || !r.tableExists(t) {
		t.Fatal("install did not enable the plugin or create its table")
	}
	if got := r.settings.Get(gadgetKey); got != "green" || !r.settings.Known(gadgetKey) {
		t.Fatalf("setting = %q, known %v", got, r.settings.Known(gadgetKey))
	}
	next := reload(t, r.db)
	if !next.manager.Enabled("gadget") || next.settings.Get(gadgetKey) != "green" {
		t.Fatal("the next boot lost the install")
	}
	info, _ := next.manager.Info("gadget")
	if info.Installed.By != someone.ID || info.Installed.At == 0 {
		t.Fatalf("install record = %+v", info.Installed)
	}
}

// A setting the plugin does not define, or a value its validator refuses,
// stops the install before anything is written.
func TestInstallRefusesSettingsThePluginDoesNotAccept(t *testing.T) {
	r := newRig(t, newGadget())
	ctx := context.Background()
	for _, values := range []map[string]string{
		{"site.name": "hijacked"},
		{gadgetOnly: "maybe"},
	} {
		err := r.manager.Install(ctx, someone, "gadget", InstallOptions{Settings: values})
		var refusal *SettingError
		if !errors.As(err, &refusal) {
			t.Fatalf("install with %v: %v", values, err)
		}
	}
	if r.manager.State("gadget") != StateAvailable || r.tableExists(t) {
		t.Fatal("a refused install left something behind")
	}
}

// A migration that fails takes the whole install back with it: no state, no
// settings, no half a schema.
func TestAFailedMigrationLeavesThePluginAvailable(t *testing.T) {
	broken := gadget{fake{name: "gadget", setup: noSetup, dir: fstest.MapFS{
		"gadget_0001_table.sql":  {Data: []byte("CREATE TABLE gadgets (id TEXT PRIMARY KEY);")},
		"gadget_0002_broken.sql": {Data: []byte("ALTER TABLE nowhere ADD COLUMN x TEXT;")},
	}}}
	r := newRig(t, broken)
	err := r.manager.Install(context.Background(), someone, "gadget", InstallOptions{
		Enable: true, Settings: map[string]string{gadgetKey: "red"},
	})
	if err == nil {
		t.Fatal("a broken migration installed")
	}
	if r.manager.State("gadget") != StateAvailable || r.tableExists(t) {
		t.Fatal("the failed install left its state or its first table")
	}
	var stored int
	_ = r.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM settings WHERE key = ?`, gadgetKey).Scan(&stored)
	if stored != 0 {
		t.Fatal("the failed install kept its settings")
	}
	// And it can be tried again once the build is fixed.
	if next := reload(t, r.db); next.manager.State("gadget") != StateAvailable {
		t.Fatal("the next boot adopted a plugin whose install rolled back")
	}
}

// Two administrators pressing install at once: one install, one refusal,
// and the migration run exactly once — the row lock, not luck.
func TestParallelInstallsInstallOnce(t *testing.T) {
	r := newRig(t, newGadget())
	ctx := context.Background()
	const racers = 8
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- r.manager.Install(ctx, Actor{ID: fmt.Sprint(i)}, "gadget", InstallOptions{Enable: true})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	won, lost := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrInstalled):
			lost++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if won != 1 || lost != racers-1 {
		t.Fatalf("won %d, lost %d", won, lost)
	}
	var recorded int
	_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 'gadget_0001_table'`).Scan(&recorded)
	if recorded != 1 {
		t.Fatalf("the migration is recorded %d times", recorded)
	}
}

// A second process against the same database: its manager is a separate
// copy, so only the database lock stands between the two installs.
func TestInstallsFromTwoProcessesInstallOnce(t *testing.T) {
	r := newRig(t, newGadget())
	other := reload(t, r.db)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i, m := range []*Manager{r.manager, other.manager} {
		wg.Add(1)
		go func(i int, m *Manager) {
			defer wg.Done()
			<-start
			results[i] = m.Install(ctx, someone, "gadget", InstallOptions{Enable: true})
		}(i, m)
	}
	close(start)
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) {
		t.Fatalf("results = %v", results)
	}
}

func TestEnableAndDisableKeepEverything(t *testing.T) {
	r := newRig(t, newGadget())
	ctx := context.Background()
	if err := r.manager.Install(ctx, someone, "gadget", InstallOptions{
		Settings: map[string]string{gadgetKey: "green"},
	}); err != nil {
		t.Fatal(err)
	}
	if r.manager.State("gadget") != StateDisabled || r.settings.Known(gadgetKey) {
		t.Fatal("installed switched off, the plugin is on")
	}
	if err := r.manager.Disable(ctx, someone, "gadget"); !errors.Is(err, ErrAlreadyInState) {
		t.Fatalf("disabling a disabled plugin: %v", err)
	}
	if err := r.manager.Enable(ctx, someone, "gadget"); err != nil {
		t.Fatal(err)
	}
	if !r.manager.Enabled("gadget") || r.settings.Get(gadgetKey) != "green" {
		t.Fatal("enable lost the settings")
	}
	if err := r.manager.Disable(ctx, someone, "gadget"); err != nil {
		t.Fatal(err)
	}
	if r.manager.Enabled("gadget") || !r.tableExists(t) {
		t.Fatal("disable took the table")
	}
	if err := r.manager.Enable(ctx, someone, "missing"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("enabling an unknown plugin: %v", err)
	}
}

func TestUninstallWithPurgeTakesTheDataAndForgetsTheMigrations(t *testing.T) {
	r := newRig(t, newGadget())
	ctx := context.Background()
	if err := r.manager.Install(ctx, someone, "gadget", InstallOptions{
		Enable: true, Settings: map[string]string{gadgetKey: "green"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.manager.Uninstall(ctx, someone, "gadget", true); err != nil {
		t.Fatal(err)
	}
	if r.manager.State("gadget") != StateAvailable || r.tableExists(t) {
		t.Fatal("purge left the plugin or its table")
	}
	if r.settings.Get(gadgetKey) != "blue" {
		t.Fatal("purge kept the setting")
	}
	// Reinstalling runs the migration again rather than trusting a record of
	// a table that is gone.
	if err := r.manager.Install(ctx, someone, "gadget", InstallOptions{Enable: true}); err != nil {
		t.Fatal(err)
	}
	if !r.tableExists(t) {
		t.Fatal("reinstalling after a purge did not recreate the table")
	}
}

// Uninstalled with its data kept, the traces are still there — and the
// tombstone is what keeps the next boot from reading them as an instance
// that never decided, and installing the plugin again behind the operator's
// back.
func TestUninstallKeepingTheDataIsNotReadopted(t *testing.T) {
	r := newRig(t, newGadget())
	ctx := context.Background()
	if err := r.manager.Install(ctx, someone, "gadget", InstallOptions{
		Enable: true, Settings: map[string]string{gadgetKey: "green"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO gadgets (id) VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	if err := r.manager.Uninstall(ctx, someone, "gadget", false); err != nil {
		t.Fatal(err)
	}
	next := reload(t, r.db)
	if next.manager.State("gadget") != StateAvailable {
		t.Fatal("the next boot re-adopted an uninstalled plugin")
	}
	if err := next.manager.Install(ctx, someone, "gadget", InstallOptions{Enable: true}); err != nil {
		t.Fatal(err)
	}
	var kept int
	_ = next.db.QueryRow(ctx, `SELECT COUNT(*) FROM gadgets`).Scan(&kept)
	if kept != 1 || next.settings.Get(gadgetKey) != "green" {
		t.Fatalf("reinstalling lost the kept data: rows %d, setting %q", kept, next.settings.Get(gadgetKey))
	}
}

// An instance that ran a plugin before plugins could be switched carries on
// running it: a recorded migration, or a stored setting, is enough.
func TestAPluginWithTracesIsAdoptedAtBoot(t *testing.T) {
	t.Run("a recorded migration", func(t *testing.T) {
		r := newRig(t, newGadget())
		ctx := context.Background()
		if _, err := r.db.Migrate(ctx, newGadget().Migrations()); err != nil {
			t.Fatal(err)
		}
		if next := reload(t, r.db); !next.manager.Enabled("gadget") {
			t.Fatal("not adopted")
		}
	})
	t.Run("a stored setting", func(t *testing.T) {
		plain := fake{name: "gadget", setup: noSetup}
		r := newRig(t, plain)
		if _, err := r.db.Exec(context.Background(),
			`INSERT INTO settings (key, value, updated_at) VALUES (?, 'green', ?)`,
			gadgetKey, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if next := reload(t, r.db); !next.manager.Enabled("gadget") {
			t.Fatal("not adopted")
		}
	})
}

// A row for a plugin this build does not carry is listed rather than
// dropped, so an operator can see why the other build's feature is gone.
func TestARowForAMissingPluginIsListed(t *testing.T) {
	r := newRig(t)
	if _, err := r.db.Exec(context.Background(),
		`INSERT INTO plugin_installs (name, state, updated_at) VALUES ('elsewhere', 'enabled', 1)`); err != nil {
		t.Fatal(err)
	}
	next := reload(t, r.db)
	list := next.manager.List()
	if len(list) != 1 || list[0].Name != "elsewhere" || !list[0].Missing {
		t.Fatalf("list = %+v", list)
	}
	if !next.manager.Enabled("elsewhere") {
		t.Fatal("the missing plugin's state was not read")
	}
}

// The manifest lists what the plugin attached, read off the extension
// points rather than written out by hand.
func TestTheManifestListsContributions(t *testing.T) {
	r := newRig(t, newGadget())
	info, err := r.manager.Info("gadget")
	if err != nil {
		t.Fatal(err)
	}
	c := info.Contributions
	if len(c.Settings) != 2 || c.Settings[0].Key != gadgetKey || !c.Purges ||
		len(c.Migrations) != 1 || c.Migrations[0] != "gadget_0001_table" {
		t.Fatalf("contributions = %+v", c)
	}
}
