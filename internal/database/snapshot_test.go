package database

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

func TestSQLiteSnapshotIsPrivatePointInTimeCopy(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO settings (key,value,updated_at) VALUES (?,?,?)`, "snapshot.test", "before", 1); err != nil {
		t.Fatal(err)
	}

	called := false
	var snapshotPath string
	err := db.ReadSnapshotBounded(ctx, 16<<20, func(snapshot Queryer) error {
		called = true
		if snapshot.Dialect() != SQLite {
			t.Fatalf("snapshot dialect = %q, want sqlite", snapshot.Dialect())
		}
		var sequence int
		var name, path string
		if err := snapshot.QueryRow(ctx, `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
			return err
		}
		snapshotPath = path
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		// Windows files carry no Unix permission bits — os.Stat reports 0666
		// for every regular file, so this assertion would fail whatever the
		// code did. The property it checks is a POSIX one, and CI runs where
		// the bits are real.
		if runtime.GOOS != "windows" {
			if got := info.Mode().Perm(); got != 0600 {
				t.Errorf("snapshot file mode = %04o, want 0600", got)
			}
		}
		if _, err := db.Exec(ctx, `UPDATE settings SET value = ? WHERE key = ?`, "after", "snapshot.test"); err != nil {
			return err
		}
		var value string
		if err := snapshot.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, "snapshot.test").Scan(&value); err != nil {
			return err
		}
		if value != "before" {
			t.Errorf("snapshot observed source update %q; wanted the earlier value", value)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read SQLite snapshot: %v", err)
	}
	if !called {
		t.Fatal("snapshot callback did not run")
	}
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Errorf("snapshot file survived callback: stat error %v", err)
	}
	var value string
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, "snapshot.test").Scan(&value); err != nil || value != "after" {
		t.Fatalf("source value = %q, %v", value, err)
	}
	if err := db.ReadSnapshotBounded(ctx, 1, func(Queryer) error {
		t.Error("oversized snapshot callback unexpectedly ran")
		return nil
	}); err == nil {
		t.Fatal("one-byte snapshot ceiling unexpectedly accepted the database")
	}
}

func TestSnapshotSchemaIntrospection(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tables, err := Tables(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !containsLine(strings.Join(tables, ","), "models") {
		t.Fatalf("table list does not include models: %#v", tables)
	}
	columns, err := Columns(ctx, db, "models")
	if err != nil {
		t.Fatal(err)
	}
	if !containsLine(strings.Join(columns, ","), "route_to_id") {
		t.Fatalf("model columns do not include route_to_id: %#v", columns)
	}
	keys, err := ForeignKeys(ctx, db, "models")
	if err != nil {
		t.Fatal(err)
	}
	foundSelfReference := false
	for _, key := range keys {
		if key.RefTable == "models" && key.Column == "route_to_id" {
			foundSelfReference = true
		}
	}
	if !foundSelfReference {
		t.Fatalf("model self-reference was not introspected: %#v", keys)
	}
	primary, err := PrimaryKeys(ctx, db, "models")
	if err != nil {
		t.Fatal(err)
	}
	if len(primary) != 1 || primary[0] != "id" {
		t.Fatalf("model primary key = %#v, want [id]", primary)
	}
	versions, err := AppliedVersions(ctx, db)
	if err != nil || len(versions) < 56 {
		t.Fatalf("applied migration versions = %d, %v", len(versions), err)
	}
}

func TestPostgresSnapshotUsesRepeatableReadWhenConfigured(t *testing.T) {
	cfg := dbtest.Postgres(t, "repeatable-read instance backup snapshots")
	db, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO settings (key,value,updated_at) VALUES (?,?,?)`, "snapshot.test", "before", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.ReadSnapshot(ctx, func(snapshot Queryer) error {
		var value string
		if err := snapshot.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, "snapshot.test").Scan(&value); err != nil {
			return err
		}
		if value != "before" {
			t.Fatalf("first snapshot read = %q, want before", value)
		}
		if _, err := db.Exec(ctx, `UPDATE settings SET value = ? WHERE key = ?`, "after", "snapshot.test"); err != nil {
			return err
		}
		if err := snapshot.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, "snapshot.test").Scan(&value); err != nil {
			return err
		}
		if value != "before" {
			t.Errorf("repeatable-read snapshot observed %q, want before", value)
		}
		return nil
	}); err != nil {
		t.Fatalf("read PostgreSQL snapshot: %v", err)
	}
}
