package database

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func coreVersions(t *testing.T) []string {
	t.Helper()
	core, err := loadMigrations(SQLite)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(core))
	for _, m := range core {
		out = append(out, m.version)
	}
	return out
}

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	tables, err := Tables(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table == name {
			return true
		}
	}
	return false
}

// A restore's schema check compares tables and columns exactly, so migrating
// the destination has to stop at what the backup's instance had run: the
// plugin it never installed must not leave its table behind, and of two
// copies of one version the first source's is the one that runs.
func TestMigrateForTakesOnlyTheVersionsTheBackupHad(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	carried := fstest.MapFS{"plugx_0001_a.sql": {Data: []byte(`CREATE TABLE plugx_a (id TEXT PRIMARY KEY);`)}}
	bundled := fstest.MapFS{"plugx_0001_a.sql": {Data: []byte(`CREATE TABLE plugx_bundled (id TEXT PRIMARY KEY);`)}}
	neverInstalled := fstest.MapFS{"plugy_0001_b.sql": {Data: []byte(`CREATE TABLE plugy_b (id TEXT PRIMARY KEY);`)}}

	versions := append(coreVersions(t), "plugx_0001_a")
	applied, err := db.MigrateFor(ctx, versions, carried, bundled, neverInstalled)
	if err != nil {
		t.Fatalf("MigrateFor: %v", err)
	}
	if got := applied[len(applied)-1]; got != "plugx_0001_a" {
		t.Errorf("last applied = %q; want the plugin's version after the core's", got)
	}
	if !tableExists(t, db, "plugx_a") {
		t.Error("the version the backup had was not applied from the first source")
	}
	if tableExists(t, db, "plugx_bundled") {
		t.Error("a later source's copy of the same version ran too")
	}
	if tableExists(t, db, "plugy_b") {
		t.Error("a plugin the backup's instance never installed was migrated")
	}
	recorded, err := AppliedVersions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if want := slices.Sorted(slices.Values(versions)); !slices.Equal(recorded, want) {
		t.Errorf("recorded versions differ from the backup's:\n got %v\nwant %v", recorded, want)
	}

	again, err := db.MigrateFor(ctx, versions, carried)
	if err != nil || len(again) != 0 {
		t.Errorf("second MigrateFor = %v, %v; want nothing applied", again, err)
	}
}

// A backup of an instance that removed a plugin keeping its data holds that
// plugin's tables and no migration to create them. Failing before anything is
// written, naming the version, is what lets the operator supply the package.
func TestMigrateForNamesWhatNothingSupplies(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	_, err := db.MigrateFor(ctx, append(coreVersions(t), "gone_0001_x"))
	if err == nil || !strings.Contains(err.Error(), "gone_0001_x") {
		t.Fatalf("MigrateFor error = %v; want one naming gone_0001_x", err)
	}
	if tableExists(t, db, "users") {
		t.Error("core tables were created although the backup could not be matched")
	}
}
