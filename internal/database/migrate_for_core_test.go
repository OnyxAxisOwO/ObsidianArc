package database

import (
	"context"
	"slices"
	"testing"
)

// An archive from an older build names fewer core versions than this binary
// has. Migrating to all of them put the destination at the newer schema, and
// the schema check then refused the archive it was meant to take.
func TestMigrateForStopsAtTheBackupsCoreVersion(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	all := coreVersions(t)
	if len(all) < 2 {
		t.Skip("needs at least two core migrations")
	}
	versions := slices.Clone(all[:len(all)-1])

	if _, err := db.MigrateFor(ctx, versions); err != nil {
		t.Fatalf("MigrateFor: %v", err)
	}
	recorded, err := AppliedVersions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if want := slices.Sorted(slices.Values(versions)); !slices.Equal(recorded, want) {
		t.Errorf("recorded versions differ from the backup's:\n got %v\nwant %v", recorded, want)
	}
}
