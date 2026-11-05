package plugin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

func TestStaleRouteCannotDispatchToReplacementPackage(t *testing.T) {
	r := newRig(t)
	old := &loaded{name: "example"}
	replacement := &loaded{name: "example"}
	r.manager.swapPackages(func(p map[string]*loaded) { p[old.name] = old })
	handler := httpx.Wrap(r.manager.routeHandler(old, arcx.Route{Pattern: "GET /api/x/example/test"}))
	r.manager.swapPackages(func(p map[string]*loaded) { p[old.name] = replacement })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/x/example/test", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("stale route status = %d; want 404", response.Code)
	}
}
