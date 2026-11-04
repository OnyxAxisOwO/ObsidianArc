package pkgtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A condition added to a query with + is a piece of a statement, and a spelling
// only one engine has is found inside it, where a check on the statement's
// start would pass it.
func TestAConditionAddedToAQueryIsChecked(t *testing.T) {
	problems := lintSource(t, "var q = \" AND ifnull(n, 0) = 1\"\n"+
		"var r = \" and ifnull(n, 0) = 1\"\n"+
		"var s = \"WHERE ifnull(n, 0) = 1\"\n")
	for _, prefix := range []string{"main.go:3", "main.go:4", "main.go:5"} {
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
}

// A sentence is not read as SQL because it happens to contain a word the rules
// know: only a piece that opens with a clause keyword is.
func TestASentenceWithAWordTheRulesKnowIsNotReadAsSQL(t *testing.T) {
	problems := lintSource(t, "var msg = \"Set the glob pattern for the export\"\n"+
		"var more = \"On failure, the rowid is logged\"\n")
	if len(problems) != 0 {
		t.Fatalf("prose was read as SQL: %v", problems)
	}
}

// The go command compiles every directory under the package except the ones it
// skips, so a statement in a subdirectory is one the backend runs.
func TestSubdirectoriesAreLinted(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), "package main\n")
	write(t, filepath.Join(dir, "store", "store.go"), "package store\n\nconst q = `SELECT ifnull(n, 0) FROM t`\n")
	problems, err := SQLProblems(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.HasPrefix(problems[0], filepath.Join("store", "store.go")+":3:") {
		t.Fatalf("want the statement in store/store.go reported, got %v", problems)
	}
}

// Directories the go command skips are not part of the package.
func TestSkippedDirectoriesAreNotLinted(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), "package main\n")
	for _, sub := range []string{"testdata", "_scratch", ".cache"} {
		write(t, filepath.Join(dir, sub, "x.go"), "package x\n\nconst q = `SELECT ifnull(n, 0) FROM t`\n")
	}
	problems, err := SQLProblems(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("a skipped directory was linted: %v", problems)
	}
}

func write(t *testing.T, name, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}
