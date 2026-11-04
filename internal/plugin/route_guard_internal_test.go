package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// A package's admin route is served by the backoffice's checks or not at all.
// With no backoffice wired, the backend must not answer it, and the request is
// refused the way an absent route is.
func TestAnAdminRouteWithNoBackofficeChecksIsRefusedAndTheBackendIsNotAsked(t *testing.T) {
	m := newRig(t).manager
	var asked atomic.Int32
	host := func(*wasm.Call, string, json.RawMessage) (any, error) {
		asked.Add(1)
		return nil, nil
	}
	pkg := parsed(t, pkgtest.Demo(t))
	backend := wasm.NewEngine(wasm.Limits{}).Load("demo", pkg.Backend, host)
	t.Cleanup(backend.Close)
	m.swapPackages(func(p map[string]*loaded) {
		p["demo"] = &loaded{name: "demo", pkg: pkg, backend: backend}
	})
	m.set("demo", record{State: StateEnabled})

	table, err := m.buildRouter(*m.pkgs.Load())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	table.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/x/demo/things", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an admin route without the backoffice's checks answered %d: %s", rec.Code, rec.Body.String())
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("the backend was reached %d times through an unguarded admin route", n)
	}
}

// Cache-Control is the backend's word on who may keep an answer. For a signed-in
// account it is dropped, so the server's no-store stands; for an anonymous call it
// is kept, marked as varying on the credentials a shared cache would ignore.
func TestABackendsCacheControlIsKeptOnlyForAnonymousAnswers(t *testing.T) {
	from := map[string]string{"Cache-Control": "public, max-age=60", "Content-Type": "application/json"}

	signedIn := http.Header{}
	signedIn.Set("Cache-Control", "no-store")
	copyBackendHeaders(signedIn, from, true)
	if got := signedIn.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("a signed-in answer lost the server's no-store: %q", got)
	}
	if got := signedIn.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type not copied for a signed-in answer: %q", got)
	}

	anonymous := http.Header{}
	copyBackendHeaders(anonymous, from, false)
	if got := anonymous.Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("an anonymous answer lost the backend's caching: %q", got)
	}
	if got := anonymous.Get("Vary"); got != "Cookie, Authorization" {
		t.Fatalf("an anonymous answer that may be cached does not vary on credentials: %q", got)
	}
}
