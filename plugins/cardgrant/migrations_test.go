package cardgrant

import (
	"io/fs"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func TestMigrationsArePortable(t *testing.T) {
	dir := cardgrantPlugin{}.Migrations()
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no migration files found")
	}
	for _, entry := range entries {
		raw, err := fs.ReadFile(dir, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range database.PortabilityProblems(string(raw)) {
			t.Errorf("%s: %s", entry.Name(), problem)
		}
	}
}

func TestPurgeIsPortable(t *testing.T) {
	dir := cardgrantPlugin{}.Purge()
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no purge files found")
	}
	for _, entry := range entries {
		raw, err := fs.ReadFile(dir, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range database.PortabilityProblems(string(raw)) {
			t.Errorf("%s: %s", entry.Name(), problem)
		}
	}
}
