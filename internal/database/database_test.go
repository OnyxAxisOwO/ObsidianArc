package database

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
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
	dsn, err := sqliteDSN("/tmp/x.db")
	if err != nil {
		t.Fatalf("plain path refused: %v", err)
	}
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
	if got, err := sqliteDSN(custom); err != nil || got != custom {
		t.Errorf("explicit DSN rewritten: %q (%v)", got, err)
	}
}

// A '?' or '#' in a plain path would be read by SQLite as the start of a query
// or a fragment, so the pragmas would vanish and a different file would open.
// A %XX escape is decoded by SQLite inside the file: URI the driver builds, so
// /srv/arc%20x/obsidian.db opens /srv/arc x/obsidian.db. The DSN is refused
// rather than built, and Open refuses before it touches the disk.
func TestSQLiteDSNRefusesPathsSQLiteWouldCut(t *testing.T) {
	for _, path := range []string{"/srv/arc#2/obsidian.db", "/srv/arc?x/obsidian.db", "/srv/arc%20x/obsidian.db"} {
		if got, err := sqliteDSN(path); err == nil {
			t.Errorf("sqliteDSN(%q) = %q, want a refusal", path, got)
		}
		if _, err := Open(context.Background(), config.Database{
			Driver: "sqlite", DSN: path, MaxOpenConns: 1, MaxIdleConns: 1,
		}); err == nil {
			t.Errorf("Open(%q) succeeded; it would open a truncated or decoded path", path)
		}
	}

	// A % that SQLite leaves alone is a plain character, and refusing it would
	// turn away installs that open correctly today.
	if got, err := sqliteDSN("/srv/50%zz/obsidian.db"); err != nil {
		t.Errorf("sqliteDSN refused a percent sign that is not an escape: %v (got %q)", err, got)
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

// Two processes booting against one database both read schema_migrations
// before either has committed, so both see the same migrations pending. The
// one that loses must skip what the other recorded, not run its DDL again and
// refuse to start. Between them each version is applied exactly once.
func TestConcurrentMigrateAppliesEachVersionOnce(t *testing.T) {
	want := len(coreVersions(t))

	t.Run("sqlite", func(t *testing.T) {
		for round := 0; round < 6; round++ {
			assertMigratedOnceBy(t, config.Database{
				Driver:       "sqlite",
				DSN:          filepath.Join(t.TempDir(), "race.db"),
				MaxOpenConns: 2,
				MaxIdleConns: 1,
			}, want)
		}
	})
	t.Run("postgres", func(t *testing.T) {
		// The first-boot collision is a window a few milliseconds wide, so one
		// round rarely hits it; each round here gets a fresh schema.
		for round := 0; round < 15; round++ {
			assertMigratedOnceBy(t, dbtest.Postgres(t, "the concurrent-migration race on PostgreSQL goes unchecked"), want)
		}
	})
}

func assertMigratedOnceBy(t *testing.T, cfg config.Database, want int) {
	t.Helper()
	ctx := context.Background()

	// Both processes boot on an empty schema, so the first-boot CREATE TABLE
	// IF NOT EXISTS is part of the race. Pre-creating the table here would hide
	// the collision on PostgreSQL's catalog that a real second instance hits.
	var handles [2]*DB
	for i := range handles {
		db, err := Open(ctx, cfg)
		if err != nil {
			t.Fatalf("open handle %d: %v", i, err)
		}
		defer db.Close()
		handles[i] = db
	}

	var (
		applied [2][]string
		errs    [2]error
		wg      sync.WaitGroup
		start   = make(chan struct{})
	)
	for i := range handles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			applied[i], errs[i] = handles[i].Migrate(ctx)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("process %d failed to boot: %v", i, err)
		}
	}
	if total := len(applied[0]) + len(applied[1]); total != want {
		t.Errorf("the two processes applied %d migrations between them, want %d, each applied once", total, want)
	}
	if again, err := handles[0].Migrate(ctx); err != nil || len(again) != 0 {
		t.Errorf("after the race a further migrate applied %v (err %v), want nothing", again, err)
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
