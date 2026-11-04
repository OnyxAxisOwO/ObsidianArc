package systembackup

import (
	"context"
	"slices"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// A populated destination used to be migrated to the archive's schema first,
// and refused only afterwards, so the refusal came after the schema had changed.
func TestPrepareRestoreRefusesAPopulatedDestinationBeforeChangingIt(t *testing.T) {
	ctx := context.Background()
	source, _, pkg := demoInstance(t)
	archive := makeArchive(t, source)

	destination := openBareDB(t)
	if _, err := destination.Migrate(ctx); err != nil {
		t.Fatalf("migrate the destination's core: %v", err)
	}
	if _, err := destination.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ('site.name', 'already in use', 1)`); err != nil {
		t.Fatal(err)
	}
	before, err := database.AppliedVersions(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}

	if err := PrepareRestore(ctx, destination, archive, testMasterKey, pkg.Migrations()); err == nil {
		t.Fatal("PrepareRestore accepted a populated destination")
	}
	after, err := database.AppliedVersions(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) {
		t.Errorf("the refused destination's schema changed:\n before %v\n after  %v", before, after)
	}
}
