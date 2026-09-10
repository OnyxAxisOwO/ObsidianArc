package dbtest

import (
	"net/url"
	"strings"
	"testing"
)

// The two pure parts of this package, which are the parts that can be wrong
// without a database to notice. Everything else here needs a real Postgres and
// is therefore only ever exercised in CI; these two are not, so they are
// checked where they can be.

func TestSearchPathIsAddedWithoutLosingWhatIsAlreadyThere(t *testing.T) {
	const dsn = "postgres://arc:arc@localhost:5432/arc_test?sslmode=disable"

	parsed, err := url.Parse(withSearchPath(t, dsn, "oa_example"))
	if err != nil {
		t.Fatalf("the result is not a URL: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("search_path"); got != "oa_example" {
		t.Errorf("search_path = %q, want oa_example", got)
	}
	// The parameter that was already on it decides whether the connection
	// works at all, so losing it would be a confusing failure rather than an
	// obvious one.
	if got := query.Get("sslmode"); got != "disable" {
		t.Errorf("sslmode = %q, want disable — the existing query was dropped", got)
	}
	if parsed.Host != "localhost:5432" || parsed.Path != "/arc_test" {
		t.Errorf("host or database changed: %s", parsed)
	}
	if password, _ := parsed.User.Password(); password != "arc" {
		t.Errorf("credentials changed: %s", parsed.User)
	}
}

func TestSearchPathIsAddedToTheKeywordForm(t *testing.T) {
	got := withSearchPath(t, "host=localhost user=arc dbname=arc_test", "oa_example")
	const want = "host=localhost user=arc dbname=arc_test search_path=oa_example"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestASchemaNameIsUsableAsAnIdentifier(t *testing.T) {
	name := schemaName(t)
	if !strings.HasPrefix(name, "oa_") {
		t.Errorf("name = %q, want the oa_ prefix that says who left it behind", name)
	}
	// Postgres truncates past 63 bytes, and a truncated name could collide
	// with another test's — which would be a test dropping a schema it is not
	// using.
	if len(name) > 63 {
		t.Errorf("name is %d bytes: %q", len(name), name)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		t.Fatalf("name %q carries %q, which would need quoting", name, r)
	}
}
