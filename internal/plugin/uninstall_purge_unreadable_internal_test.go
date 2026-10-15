package plugin

import (
	"context"
	"errors"
	"testing"
)

// An unreadable package has no purge scripts, migration records or settings
// the manager can walk, so a purge would report success and leave all three
// behind for a reinstall to reuse. It is refused instead, and the package stays
// installed, so the operator can still remove it without the purge.
func TestAnUnreadablePackageCannotBePurged(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.manager.InstallPackage(ctx, someone, parsed(t, packageOf(t, "orphan", nil)), SourceUpload,
		InstallOptions{Enable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(ctx, `UPDATE plugin_packages SET archive = ? WHERE name = ?`, []byte("not a zip"), "orphan"); err != nil {
		t.Fatal(err)
	}

	m := reload(t, r.db).manager
	if err := m.Uninstall(ctx, someone, "orphan", true); !errors.Is(err, ErrPreflight) {
		t.Fatalf("a purge of an unreadable package: %v; want it refused", err)
	}
	if err := m.Uninstall(ctx, someone, "orphan", false); err != nil {
		t.Fatalf("the refused purge removed the package: %v", err)
	}
}
