package plugin

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fake struct {
	name  string
	dir   fs.FS
	setup func(*Host) error
}

func (f fake) Name() string        { return f.name }
func (f fake) Setup(h *Host) error { return f.setup(h) }
func (f fake) Migrations() fs.FS   { return f.dir }
func (f fake) Manifest() Manifest  { return Manifest{Version: "1"} }

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

// What the host hands out itself follows the plugin's state on every
// request: its public routes answer 404 and its origins leave the policy
// while it is switched off, with nothing re-registered.
func TestHostRoutesAndOriginsFollowTheGate(t *testing.T) {
	on := false
	base := &Host{Mux: http.NewServeMux(), Gate: func(name string) bool { return name == "gadget" && on }}
	h := *base
	h.share()
	h.name = "gadget"
	h.Handle("GET /api/gadget", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.AllowOrigin(func() string { return "https://gadget.example.com" })
	base.shared = h.shared

	status := func() int {
		recorder := httptest.NewRecorder()
		base.Mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/gadget", nil))
		return recorder.Code
	}
	if status() != http.StatusNotFound || len(base.Origins()) != 0 {
		t.Fatal("a switched-off plugin's route or origin is live")
	}
	on = true
	if status() != http.StatusTeapot || len(base.Origins()) != 1 {
		t.Fatal("switching the plugin on did not bring its route and origin back")
	}
	if routes := base.publicRoutes("gadget"); len(routes) != 1 || routes[0].Pattern != "GET /api/gadget" {
		t.Fatalf("public routes = %v", routes)
	}
}
