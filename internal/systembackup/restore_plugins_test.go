package systembackup

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
)

// openBareDB is a destination as `restore-backup` finds one: opened, nothing
// migrated.
func openBareDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "arc.db"), MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// demoInstance is a source instance that ran the demo plugin's migrations, and
// kept its data. Whether the package itself is still installed is the caller's.
func demoInstance(t *testing.T) (*database.DB, []byte, *arcx.Package) {
	t.Helper()
	raw := pkgtest.Demo(t)
	pkg, err := arcx.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	source := openBackupTestDB(t, t.TempDir())
	if _, err := source.Migrate(context.Background(), pkg.Migrations()); err != nil {
		t.Fatalf("migrate demo: %v", err)
	}
	if _, err := source.Exec(context.Background(),
		`INSERT INTO demo_things (id, name, created_at) VALUES ('thing-1', 'survives the restore', 1)`); err != nil {
		t.Fatal(err)
	}
	return source, raw, pkg
}

func installDemoPackage(t *testing.T, db *database.DB, raw []byte, pkg *arcx.Package) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`INSERT INTO plugin_packages (name, version, sha256, source, archive, added_at, added_by) VALUES (?, ?, ?, 'upload', ?, 1, 'test')`,
		pkg.Manifest.Name, pkg.Manifest.Version, pkg.SHA256, raw); err != nil {
		t.Fatal(err)
	}
}

func demoThingName(t *testing.T, db *database.DB) string {
	t.Helper()
	var name string
	if err := db.QueryRow(context.Background(), `SELECT name FROM demo_things WHERE id = 'thing-1'`).Scan(&name); err != nil {
		t.Fatalf("read restored demo row: %v", err)
	}
	return name
}

// The reason PrepareRestore exists: the archive of an instance with a package
// installed holds that package's tables and columns, and a fresh database has
// nothing that would create them. The package is in the archive, so nothing
// else is needed.
func TestRestoreRecreatesThePluginTablesFromThePackageTheArchiveCarries(t *testing.T) {
	ctx := context.Background()
	source, raw, pkg := demoInstance(t)
	installDemoPackage(t, source, raw, pkg)
	archive := makeArchive(t, source)

	destination := openBareDB(t)
	if err := PrepareRestore(ctx, destination, archive, testMasterKey); err != nil {
		t.Fatalf("PrepareRestore: %v", err)
	}
	if err := RestoreArchive(ctx, destination, archive, testMasterKey); err != nil {
		t.Fatalf("RestoreArchive: %v", err)
	}
	if got := demoThingName(t, destination); got != "survives the restore" {
		t.Errorf("restored demo row = %q", got)
	}
	var restored []byte
	if err := destination.QueryRow(ctx, `SELECT archive FROM plugin_packages WHERE name = 'demo'`).Scan(&restored); err != nil || !bytes.Equal(restored, raw) {
		t.Errorf("the package was not restored intact: %v", err)
	}
	if _, err := destination.Exec(ctx, `UPDATE users SET handle = handle`); err != nil {
		t.Errorf("the column the package added to users is missing: %v", err)
	}
}

// An instance that removed a plugin keeping its data has the tables and no
// package: the SQL that made them has to come from somewhere else, and when it
// does not, the failure names what is missing before a row is written.
func TestRestoreOfARemovedPluginNeedsItsMigrationsSuppliedAndSaysWhich(t *testing.T) {
	ctx := context.Background()
	source, _, pkg := demoInstance(t)
	archive := makeArchive(t, source)

	bare := openBareDB(t)
	err := PrepareRestore(ctx, bare, archive, testMasterKey)
	if err == nil || !strings.Contains(err.Error(), "demo_0001_things") {
		t.Fatalf("PrepareRestore without the package = %v; want an error naming demo_0001_things", err)
	}

	supplied := openBareDB(t)
	if err := PrepareRestore(ctx, supplied, archive, testMasterKey, pkg.Migrations()); err != nil {
		t.Fatalf("PrepareRestore with the migrations supplied: %v", err)
	}
	if err := RestoreArchive(ctx, supplied, archive, testMasterKey); err != nil {
		t.Fatalf("RestoreArchive: %v", err)
	}
	if got := demoThingName(t, supplied); got != "survives the restore" {
		t.Errorf("restored demo row = %q", got)
	}
}

// What the deployment bundles is offered for restores whether or not the
// archive's instance ever used it. Migrating the destination with all of it
// would give the destination tables the archive lacks, and the schema check
// would then refuse an archive that is perfectly good.
func TestRestoreIgnoresBundledPluginsTheArchivesInstanceNeverRan(t *testing.T) {
	ctx := context.Background()
	_, _, pkg := demoInstance(t)
	source := openBackupTestDB(t, t.TempDir())
	archive := makeArchive(t, source)

	destination := openBareDB(t)
	if err := PrepareRestore(ctx, destination, archive, testMasterKey, pkg.Migrations()); err != nil {
		t.Fatalf("PrepareRestore: %v", err)
	}
	if err := RestoreArchive(ctx, destination, archive, testMasterKey); err != nil {
		t.Fatalf("RestoreArchive: %v", err)
	}
	if _, err := destination.Exec(ctx, `SELECT 1 FROM demo_things`); err == nil {
		t.Error("the destination has a table for a plugin the source never ran")
	}
}

// Migrations that came out of an archive are SQL: nothing runs until the key
// says the archive is this instance's.
func TestPrepareRestoreRunsNothingForTheWrongKey(t *testing.T) {
	ctx := context.Background()
	source, raw, pkg := demoInstance(t)
	installDemoPackage(t, source, raw, pkg)
	archive := makeArchive(t, source)

	destination := openBareDB(t)
	if err := PrepareRestore(ctx, destination, archive, []byte("not-the-instance-key-at-all-nope")); err == nil {
		t.Fatal("PrepareRestore accepted the wrong key")
	}
	if _, err := destination.Exec(ctx, `SELECT 1 FROM users`); err == nil {
		t.Error("the destination was migrated although the key was wrong")
	}
}
