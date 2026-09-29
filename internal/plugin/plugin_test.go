package plugin

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

type fake struct {
	name  string
	dir   fs.FS
	setup func(*Host) error
}

func (f fake) Name() string        { return f.name }
func (f fake) Setup(h *Host) error { return f.setup(h) }
func (f fake) Migrations() fs.FS   { return f.dir }

func noSetup(*Host) error { return nil }

func isolate(t *testing.T) {
	saved := registered
	registered = nil
	t.Cleanup(func() { registered = saved })
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("did not panic")
		}
	}()
	fn()
}

// Setup order is by name, not by whichever order the linker ran the init
// functions in, so two builds with the same plugins attach them the same way.
func TestPluginsAreSetUpInNameOrder(t *testing.T) {
	isolate(t)
	var order []string
	record := func(name string) func(*Host) error {
		return func(*Host) error { order = append(order, name); return nil }
	}
	Register(fake{name: "zeta", setup: record("zeta")})
	Register(fake{name: "alpha", setup: record("alpha")})
	if err := SetupAll(&Host{}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "alpha" || order[1] != "zeta" {
		t.Fatalf("setup order = %v", order)
	}
	if names := Names(); len(names) != 2 || names[0] != "alpha" {
		t.Fatalf("names = %v", names)
	}
}

func TestABadOrRepeatedNameIsRefusedAtRegistration(t *testing.T) {
	isolate(t)
	Register(fake{name: "once", setup: noSetup})
	mustPanic(t, func() { Register(fake{name: "once", setup: noSetup}) })
	mustPanic(t, func() { Register(fake{name: "Has-Caps", setup: noSetup}) })
	mustPanic(t, func() { Register(fake{name: "", setup: noSetup}) })
}

// The first failure stops the rest and names the plugin: a server with half
// a plugin attached is worse than one that refuses to start.
func TestASetupFailureNamesThePlugin(t *testing.T) {
	isolate(t)
	boom := errors.New("boom")
	ran := false
	Register(fake{name: "broken", setup: func(*Host) error { return boom }})
	Register(fake{name: "later", setup: func(*Host) error { ran = true; return nil }})
	err := SetupAll(&Host{})
	if !errors.Is(err, boom) || err.Error() != "plugin broken: boom" {
		t.Fatalf("err = %v", err)
	}
	if ran {
		t.Fatal("a plugin after the failing one was still set up")
	}
}

func TestMigrationsSkipsPluginsWithNone(t *testing.T) {
	isolate(t)
	Register(fake{name: "tables", setup: noSetup, dir: fstest.MapFS{"tables_0001_x.sql": {}}})
	Register(fake{name: "plain", setup: noSetup})
	if got := Migrations(); len(got) != 1 {
		t.Fatalf("migration sources = %d, want 1", len(got))
	}
}

func TestOriginsAreAskedEveryTime(t *testing.T) {
	h := &Host{}
	if h.Origins() != nil {
		t.Fatal("a host with no plugin origins reported some")
	}
	current := ""
	h.AllowOrigin(func() string { return current })
	if len(h.Origins()) != 0 {
		t.Fatal("an origin that answered empty was reported")
	}
	current = "https://service.example.com"
	if got := h.Origins(); len(got) != 1 || got[0] != current {
		t.Fatalf("origins = %v", got)
	}
}
