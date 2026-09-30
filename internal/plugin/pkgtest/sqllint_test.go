package pkgtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lintSource(t *testing.T, src string) []string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\n"+src), 0o644); err != nil {
		t.Fatal(err)
	}
	// A test file's SQL is the test's own business and is not read.
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package main\n\nconst q = `INSERT OR IGNORE INTO t VALUES (1)`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := SQLProblems(dir)
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

func TestPortableStatementsPass(t *testing.T) {
	problems := lintSource(t, "const a = `SELECT id FROM users WHERE username_lower = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`\n"+
		"const b = `INSERT INTO t (id) VALUES (?) ON CONFLICT (id) DO UPDATE SET id = excluded.id`\n"+
		"var c = \"UPDATE t SET n = COALESCE(n, 0) + 1 WHERE id = ?\"\n"+
		"var notSQL = \"datetime is a word in a sentence\"\n")
	if len(problems) != 0 {
		t.Fatalf("portable SQL was flagged: %v", problems)
	}
}

func TestEngineSpecificSpellingsAreFlaggedWhereverTheStatementIsWritten(t *testing.T) {
	problems := lintSource(t, "const a = `INSERT OR IGNORE INTO t (id) VALUES (?)`\n"+
		"var b = \"SELECT datetime(created_at / 1000, 'unixepoch') FROM t\"\n"+
		// Joined with + the statement is one string, and is read as one.
		"var c = \"SELECT ifnull(n, 0) \" + \"FROM t\"\n"+
		"const d = `SELECT n::text FROM t`\n"+
		"const e = `-- the comment may say INSERT OR IGNORE\nSELECT 1 FROM t`\n")
	want := []string{"main.go:3", "main.go:4", "main.go:5", "main.go:6"}
	for _, prefix := range want {
		found := false
		for _, p := range problems {
			if strings.HasPrefix(p, prefix) {
				found = true
			}
		}
		if !found {
			t.Errorf("no problem reported at %s; got %v", prefix, problems)
		}
	}
	for _, p := range problems {
		if strings.HasPrefix(p, "main.go:7") {
			t.Errorf("a comment was read as SQL: %s", p)
		}
	}
}

// The example plugin is what authors copy from, so its queries are the ones
// that must be portable.
func TestTheDemoPluginsQueriesArePortable(t *testing.T) {
	problems, err := SQLProblems(filepath.Join(SDKDir(), "examples", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("the demo plugin's SQL is not portable: %v", problems)
	}
}
