package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

func TestRebind(t *testing.T) {
	const query = `SELECT id FROM users WHERE username_lower = ? AND status = ?`

	if got := Rebind(SQLite, query); got != query {
		t.Errorf("SQLite query should be untouched:\n got %q\nwant %q", got, query)
	}

	want := `SELECT id FROM users WHERE username_lower = $1 AND status = $2`
	if got := Rebind(Postgres, query); got != want {
		t.Errorf("Postgres rebind:\n got %q\nwant %q", got, want)
	}

	// A query with no placeholders must come back byte-identical rather than
	// through the builder, because most queries have none.
	const plain = `SELECT COUNT(*) FROM users`
	if got := Rebind(Postgres, plain); got != plain {
		t.Errorf("query without placeholders changed: %q", got)
	}
}

func TestSplitStatements(t *testing.T) {
	src := `
-- a comment; with a semicolon in it
CREATE TABLE a (id TEXT PRIMARY KEY);

CREATE TABLE b (
    note TEXT NOT NULL DEFAULT 'has ; and ''quotes'''
);
`
	got := splitStatements(src)
	if len(got) != 2 {
		t.Fatalf("want 2 statements, got %d: %#v", len(got), got)
	}
	if want := "CREATE TABLE a (id TEXT PRIMARY KEY)"; !containsLine(got[0], want) {
		t.Errorf("first statement %q does not contain %q", got[0], want)
	}
	if !containsLine(got[1], "has ; and ''quotes''") {
		t.Errorf("second statement lost its quoted semicolon: %q", got[1])
	}
}

func containsLine(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestSQLiteDSNPragmas(t *testing.T) {
	dsn := sqliteDSN("/tmp/x.db")
	for _, want := range []string{
		"journal_mode%28WAL%29",
		"busy_timeout%285000%29",
		"foreign_keys%281%29",
		"_txlock=immediate",
	} {
		if !containsLine(dsn, want) {
			t.Errorf("DSN %q is missing %q", dsn, want)
		}
	}

	// A caller who wrote their own file: URL keeps full control.
	custom := "file:/tmp/y.db?_pragma=foreign_keys(0)"
	if got := sqliteDSN(custom); got != custom {
		t.Errorf("explicit DSN rewritten: %q", got)
	}
}

// Migrate is the one piece of Phase 1 with real consequences if it drifts:
// it runs on every boot, against a live database. This exercises it end to
// end on SQLite, including the "run twice, apply once" property that makes a
// restart safe.
func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("first migrate applied nothing")
	}

	again, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second migrate re-applied %v", again)
	}

	for _, table := range []string{"user_groups", "users", "sessions", "settings", "user_preferences"} {
		var count int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Errorf("table %s is not queryable: %v", table, err)
		}
	}
}

// Foreign keys are off by default in SQLite, and the schema depends on them.
// A DSN typo would silently disable every ON DELETE CASCADE in the project,
// so it is worth asserting rather than assuming.
func TestSQLiteEnforcesForeignKeys(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	_, err := db.Exec(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		"s1", "nobody", 0, 0, 0)
	if err == nil {
		t.Fatal("inserted a session for a user that does not exist")
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	sentinel := context.Canceled
	err := db.Tx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
			"site.name", "Arc", 1); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("want the callback's error back, got %v", err)
	}

	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM settings`).Scan(&count); err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if count != 0 {
		t.Errorf("rolled-back insert survived: %d rows", count)
	}
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 2,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
