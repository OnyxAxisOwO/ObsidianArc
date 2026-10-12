package arcx

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// The migrations and purge scripts run SQL as the database's owner, outside
// every host permission check, so the manifest has to ask for db for them to
// run at all. The install dialog presents db as the broad grant it is.
func TestSQLRunsOnlyForAPackageThatAsksForDB(t *testing.T) {
	entries := goodEntries()
	entries["manifest.json"] = strings.Replace(goodManifest, `"permissions": ["db"],`, `"permissions": ["users"],`, 1)
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("migrations and a purge script without db were accepted: %v", err)
	}

	delete(entries, "migrations/0001_demo.sql")
	delete(entries, "purge/0001_demo.sql")
	if _, err := Parse(zipOf(t, entries)); err != nil {
		t.Fatalf("a package with no SQL and no db was refused: %v", err)
	}
}

// A pattern with an unmatched ')' closes the group Whole wraps it in, so the
// anchors no longer cover it. The bare pattern does not compile, which is the
// one place that can be seen.
func TestAPatternCannotCloseItsGroupEarly(t *testing.T) {
	for _, pattern := range []string{"go-[0-9]+)|(.+", "a)|(?:.*", "a)(b"} {
		if _, err := Whole(pattern); err == nil {
			t.Errorf("Whole(%q) was accepted", pattern)
		}
	}
	re, err := Whole("go-[0-9]+|x")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"go-42", "x"} {
		if !re.MatchString(s) {
			t.Errorf("Whole did not match %q", s)
		}
	}
	if re.MatchString("anything-at-all") {
		t.Error("Whole matched a string its pattern does not describe")
	}
}

func manifestWithRoute(pattern string) string {
	return `{"arcx":1,"name":"demo","version":"1","title":{"en":"Demo"},"requires":{"api":1},"backend":"plugin.wasm",` +
		`"routes":[{"pattern":` + strconv.Quote(pattern) + `,"access":"public"}]}`
}

// A public route must not reach the admin namespace. The mux decodes escapes
// in a literal segment, and a wildcard first segment matches "admin" too, so
// the text prefix test alone does not hold.
func TestAPublicRouteCannotReachTheAdminNamespace(t *testing.T) {
	for _, pattern := range []string{
		"POST /api/%61dmin/zzz",
		"PUT /api/%61dmin/x/a/settings",
		"GET /api/{rest...}",
		"GET /api/{a}/{b}",
		"GET /api/admin",
		"GET /api/",
	} {
		_, err := ParseManifest([]byte(manifestWithRoute(pattern)))
		if err == nil || !strings.Contains(err.Error(), "public route") {
			t.Errorf("%s was accepted as a public route: %v", pattern, err)
		}
	}
	if _, err := ParseManifest([]byte(manifestWithRoute("GET /api/x/demo/a"))); err != nil {
		t.Errorf("an ordinary public route was refused: %v", err)
	}
}

func consoleWithPermission(permission string) string {
	return `{"arcx":1,"name":"demo","version":"1","title":{"en":"Demo"},"requires":{"api":1},"backend":"plugin.wasm",` +
		`"permissions":["users"],"console":[{"name":"demo ping","summary":{"en":"Ping"},"permission":` +
		strconv.Quote(permission) + `}]}`
}

// "anyone" lets every account the terminal admits run a command whose backend
// acts with the package's own permissions, and the backend cannot tell who
// asked. A command has to name a grant or super_admin.
func TestAConsoleCommandCannotBeOpenToEveryTerminalUser(t *testing.T) {
	if _, err := ParseManifest([]byte(consoleWithPermission("anyone"))); err == nil {
		t.Fatal("a package console command with permission anyone was accepted")
	}
	for _, permission := range []string{"users", "super_admin", "users,cards"} {
		if _, err := ParseManifest([]byte(consoleWithPermission(permission))); err != nil {
			t.Errorf("permission %q was refused: %v", permission, err)
		}
	}
}
