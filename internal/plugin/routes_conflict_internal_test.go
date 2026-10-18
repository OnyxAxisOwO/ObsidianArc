package plugin

import (
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

// Two packages that claim one route cannot both be served. That is a refusal the
// install reports as a preflight error, and nothing about the table changes.
func TestTwoPackagesClaimingOneRouteIsARefusalNotACrash(t *testing.T) {
	r := newRig(t)
	route := arcx.Route{Pattern: "GET /api/conflict", Access: "public"}
	pkgs := map[string]*loaded{
		"one": {name: "one", pkg: &arcx.Package{Manifest: &arcx.Manifest{Routes: []arcx.Route{route}}}},
		"two": {name: "two", pkg: &arcx.Package{Manifest: &arcx.Manifest{Routes: []arcx.Route{route}}}},
	}
	_, err := r.manager.buildRouter(pkgs)
	var refusal *PreflightError
	if !errors.As(err, &refusal) {
		t.Fatalf("a route claimed twice: %v; want a preflight refusal", err)
	}
}

// A fault while the table is built is a bug and must surface as one. Reporting it
// as a route that conflicts would send the operator looking at the wrong package.
func TestAFaultWhileBuildingTheRoutesIsNotCalledAConflict(t *testing.T) {
	r := newRig(t)
	defer func() {
		if recover() == nil {
			t.Fatal("a package with no manifest was reported as a route problem instead of failing")
		}
	}()
	_, _ = r.manager.buildRouter(map[string]*loaded{"broken": {name: "broken"}})
}
