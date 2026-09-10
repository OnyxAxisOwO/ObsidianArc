// Package dbtest hands a test its own Postgres to work in.
//
// Two packages exercise the real schema — the migration test here and the
// concurrent-bootstrap test in internal/server — and CI gives them one
// database between them. `go test ./...` runs package binaries in parallel,
// so both were dropping and recreating the same tables at the same time, and
// each carried its own hand-written list of tables to drop first. That list
// is the part that rots: it was written when there were fewer tables, so a
// re-run met a `sessions` that the drop list had never heard of and the
// migration failed on a relation that already existed.
//
// A schema of its own fixes both halves at once. Nothing is shared, so
// nothing has to be dropped by name, and a table added to a migration cannot
// leave a stale copy behind for the next run to trip over.
package dbtest

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DSNVariable is where a Postgres to test against is named. Without it every
// caller skips, which is what keeps the suite runnable on a machine with no
// database installed.
const DSNVariable = "OBSIDIAN_TEST_POSTGRES_DSN"

// Postgres returns a database configuration pointed at an empty schema of
// this test's own, and drops the schema when the test ends.
//
// why is added to the skip message: a skipped Postgres test usually means the
// thing it was covering went unchecked, and the caller knows what that is.
func Postgres(t *testing.T, why string) config.Database {
	t.Helper()

	dsn := os.Getenv(DSNVariable)
	if dsn == "" {
		t.Skipf("set %s — %s", DSNVariable, why)
	}

	schema := schemaName(t)

	admin := connect(t, dsn)
	// Dropped first as well as last: a test killed part way through leaves
	// its schema behind, and the next run with the same name should not
	// inherit it.
	exec(t, admin, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	exec(t, admin, `CREATE SCHEMA `+schema)
	_ = admin.Close()

	t.Cleanup(func() {
		cleanup := connect(t, dsn)
		defer cleanup.Close()
		exec(t, cleanup, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})

	return config.Database{
		Driver:       "postgres",
		DSN:          withSearchPath(t, dsn, schema),
		MaxOpenConns: 8,
		MaxIdleConns: 4,
	}
}

// schemaName is derived from the test's own name so a schema left behind by a
// crash says which test left it.
func schemaName(t *testing.T) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, t.Name())

	// Postgres truncates an identifier at 63 bytes, and a truncated one could
	// collide with another test's; the tail is what differs between subtests,
	// so it is the end that is kept.
	const room = 56
	if len(safe) > room {
		safe = safe[len(safe)-room:]
	}
	return "oa_" + safe
}

// withSearchPath points a DSN at one schema, in whichever of the two shapes
// libpq accepts it.
func withSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("%s is not a usable URL: %v", DSNVariable, err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	// The keyword/value form, where parameters are separated by spaces.
	return dsn + " search_path=" + schema
}

func connect(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	if err := pool.PingContext(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("connect to the test database: %v", err)
	}
	return pool
}

func exec(t *testing.T, pool *sql.DB, statement string) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}
