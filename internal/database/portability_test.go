package database

import (
	"context"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// The migrations run on two engines from one set of files. Nothing checks
// that at compile time, and the failure mode is a deployment that will not
// start — so the syntax that only works on one of them is checked here.
//
// This is a lint, not a substitute for running against Postgres. That is what
// TestPostgresMigrations below does, when a database is available.
func TestMigrationsAvoidEngineSpecificSyntax(t *testing.T) {
	banned := []struct {
		pattern *regexp.Regexp
		why     string
	}{
		{regexp.MustCompile(`(?i)\bAUTOINCREMENT\b`), "SQLite only; identifiers here are ULIDs"},
		{regexp.MustCompile(`(?i)\b(BIG)?SERIAL\b`), "Postgres only; identifiers here are ULIDs"},
		{regexp.MustCompile(`(?i)\bCURRENT_TIMESTAMP\b`), "spelled differently per engine; timestamps here are epoch milliseconds from Go"},
		{regexp.MustCompile(`(?i)\bNOW\(\)`), "Postgres only"},
		{regexp.MustCompile(`(?i)\bILIKE\b`), "Postgres only; use LOWER(...) LIKE"},
		{regexp.MustCompile(`(?i)\bJSONB\b`), "Postgres only; JSON is stored as TEXT"},
		{regexp.MustCompile(`(?i)\bTIMESTAMPTZ\b`), "Postgres only"},
		{regexp.MustCompile(`(?i)\bDATETIME\b`), "SQLite only"},
		{regexp.MustCompile(`(?i)\bAUTO_INCREMENT\b`), "MySQL only"},
		{regexp.MustCompile(`(?i)\bNULLS\s+(FIRST|LAST)\b`), "Postgres only"},
		{regexp.MustCompile(`(?i)\bWITHOUT\s+ROWID\b`), "SQLite only"},
		// BLOB and BYTEA are the one real difference, and the migration
		// runner substitutes %BLOB% for whichever the engine wants. The token
		// itself is stripped before these run, so only a literal spelling
		// trips them.
		{regexp.MustCompile(`(?i)\bBYTEA\b`), "use the %BLOB% token"},
		{regexp.MustCompile(`(?i)\bBLOB\b`), "use the %BLOB% token"},
	}

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

		// Comments explain the rules, so they are allowed to name them; the
		// dialect token is a substitution rather than a spelling.
		body := strings.ReplaceAll(stripComments(string(raw)), "%BLOB%", "")

		for _, rule := range banned {
			if match := rule.pattern.FindString(body); match != "" {
				t.Errorf("%s: %q is not portable — %s", entry.Name(), match, rule.why)
			}
		}
	}
}

func stripComments(sql string) string {
	var out strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if index := strings.Index(line, "--"); index >= 0 {
			line = line[:index]
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
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
	dsn := os.Getenv("OBSIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set OBSIDIAN_TEST_POSTGRES_DSN to run the Postgres migration test")
	}

	ctx := context.Background()
	db, err := Open(ctx, config.Database{Driver: "postgres", DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	// A clean slate, so the test says something on a second run.
	for _, table := range []string{
		"schema_migrations", "usage_counters", "usage_records", "quota_policies",
		"attachments", "messages", "conversations", "group_models", "models",
		"providers", "user_preferences", "settings", "sessions", "users", "user_groups",
	} {
		if _, err := db.Exec(ctx, `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

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
