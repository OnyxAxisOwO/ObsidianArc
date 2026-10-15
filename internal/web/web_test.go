package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The embedded bundle is a build product, so these tests exercise it only when
// a frontend has been built. Without one, Handler answers with the "not built"
// page and there is no asset tree to test against.
func builtHandler(t *testing.T) http.Handler {
	t.Helper()
	if _, err := fs.Stat(distFS, "dist/index.html"); err != nil {
		t.Skip("frontend not built: run the web build to exercise the embedded bundle")
	}
	handler, err := Handler(Options{})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// A bundle the previous release built is not on disk any more. The tab that
// still asks for it must hear 404: the shell is HTML, and a module script that
// receives HTML fails with a MIME error that a reload is the only way out of.
func TestAMissingBundleIsNotAnsweredWithTheShell(t *testing.T) {
	handler := builtHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/Chat-0123abcd.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q: the shell was served for a missing bundle", ct)
	}
}

// Deep links are why the fallback exists at all: the router owns the path, so
// it must still get the shell.
func TestAnApplicationRouteStillGetsTheShell(t *testing.T) {
	handler := builtHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/c/01HZX0000000000000000000", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want the shell", ct)
	}
}

// The fix narrows the 404 to paths that are really missing. A bundle that is
// on disk still comes back as itself, with the cache header its fingerprint
// earns.
func TestAnExistingBundleIsServedAsItself(t *testing.T) {
	handler := builtHandler(t)

	entries, err := fs.ReadDir(distFS, "dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	var name string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			name = entry.Name()
			break
		}
	}
	if name == "" {
		t.Skip("the build produced no script bundle to request")
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/"+name, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for %s", rec.Code, name)
	}
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q: the bundle came back as the shell", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want the fingerprinted-asset policy", cc)
	}
}
