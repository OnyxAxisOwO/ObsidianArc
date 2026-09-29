package database

import (
	"context"
	"io/fs"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

// The lint itself is PortabilityProblems, in portable.go, so a plugin's
// tests can hold its migrations to the same list.
//
// This is a lint, not a substitute for running against Postgres. That is what
// TestPostgresMigrations below does, when a database is available.
func TestMigrationsAvoidEngineSpecificSyntax(t *testing.T) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations found")
	}

	for _, entry := range entries {
		raw, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, problem := range PortabilityProblems(string(raw)) {
			t.Errorf("%s: %s", entry.Name(), problem)
		}
	}
}

func TestPortabilityLintCatchesEngineSpecificSQL(t *testing.T) {
	if got := PortabilityProblems("CREATE TABLE t (id BIGSERIAL PRIMARY KEY)"); len(got) == 0 {
		t.Error("BIGSERIAL passed the lint")
	}
	if got := PortabilityProblems("-- no AUTOINCREMENT here\nCREATE TABLE t (data %BLOB%)"); len(got) != 0 {
		t.Errorf("a comment and the dialect token were flagged: %v", got)
	}
}

// Every migration has to apply cleanly to a fresh database and be a no-op on
// a second run. Broken ordering only shows up here.
func TestMigrationsApplyInOrder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	entries, _ := fs.ReadDir(migrationsFS, "migrations")
	if len(applied) != len(entries) {
		t.Fatalf("applied %d of %d migrations", len(applied), len(entries))
	}
	for i := 1; i < len(applied); i++ {
		if applied[i] <= applied[i-1] {
			t.Errorf("migrations ran out of order: %s before %s", applied[i-1], applied[i])
		}
	}
}

// Runs the real schema against a real Postgres when one is available, which
// is the only way to know the dialect handling actually holds. Skipped
// otherwise, so the suite stays runnable with nothing installed.
//
//	OBSIDIAN_TEST_POSTGRES_DSN=postgres://user:pass@localhost:5432/arc_test go test ./internal/database/
func TestPostgresMigrations(t *testing.T) {
	// An empty schema of this test's own rather than the shared database with
	// a list of tables dropped from it: the list only ever named the tables
	// that existed when it was written, and one it had never heard of was
	// enough to fail the migration that tried to create it again.
	ctx := context.Background()
	db, err := Open(ctx, dbtest.Postgres(t, "the dialect handling then goes unexercised"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if again, err := db.Migrate(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second migrate: applied %v, err %v", again, err)
	}

	// The two statements the whole quota design rests on, which is where a
	// dialect difference would actually hurt.
	if _, err := db.Exec(ctx,
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, ?, ?, 1, 0, 0)`, "u:test", "5h", 0); err != nil {
		t.Fatalf("insert counter: %v", err)
	}

	var requests int64
	err = db.QueryRow(ctx,
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, ?, ?, 1, 0, 0)
		 ON CONFLICT (scope_key, window_kind, window_start) DO UPDATE SET
		   requests = usage_counters.requests + excluded.requests
		 RETURNING requests`, "u:test", "5h", 0).Scan(&requests)
	if err != nil {
		t.Fatalf("upsert with RETURNING: %v", err)
	}
	if requests != 2 {
		t.Errorf("counter = %d after two increments, want 2", requests)
	}
}
