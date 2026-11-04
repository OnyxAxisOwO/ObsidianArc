package checkin

import (
	"go/build"
	"slices"
	"testing"
)

// The import is the only thing that puts the zone database in the binary, and
// a test host always has a copy of its own, so no zone test would notice the
// import going.
func TestZoneDatabaseIsCompiledIn(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(pkg.Imports, "time/tzdata") {
		t.Fatal("checkin no longer imports time/tzdata: a host without a zone database would count days in UTC")
	}
}
