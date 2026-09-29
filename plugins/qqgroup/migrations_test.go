package qqgroup

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// The two migrations came out of the core with their versions, which is the
// whole of the upgrade story: a database that ran them as core already has
// both rows in schema_migrations, so compiling the plugin in applies nothing.
// Renaming either would make that database run it again and fail on a column
// that already exists.
func TestMigrationsKeepTheirCoreVersions(t *testing.T) {
	entries, err := fs.ReadDir(qqPlugin{}.Migrations(), ".")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, entry := range entries {
		got[entry.Name()] = true
	}
	for _, want := range []string{"0015_user_qq.sql", "0061_group_departures.sql"} {
		if !got[want] {
			t.Errorf("%s is missing or renamed; databases that ran it as core would run it twice", want)
		}
	}
}

func TestMigrationsArePortable(t *testing.T) {
	dir := qqPlugin{}.Migrations()
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		t.Fatal(err)
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

// An instance that ran the core's migrations before they moved: the plugin's
// directory joins the sequence and finds nothing to do.
func TestADatabaseMigratedAsCoreAppliesNothingNew(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "legacy.db"), MaxOpenConns: 2, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Migrate(ctx, qqPlugin{}.Migrations()); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	again, err := db.Migrate(ctx, qqPlugin{}.Migrations())
	if err != nil || len(again) != 0 {
		t.Fatalf("second migrate applied %v (err %v)", again, err)
	}
	// And the same database booted without the plugin keeps running: the
	// columns stay where they are and the core reads around them.
	if again, err := db.Migrate(ctx); err != nil || len(again) != 0 {
		t.Fatalf("core-only migrate applied %v (err %v)", again, err)
	}
}
