package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// purgingPackage is a package with no backend whose SQL is one migration and one
// purge script, so an uninstall-with-data shows which version's script ran. A
// purge needs a migration to take back, so the package carries one.
func purgingPackage(t *testing.T, name, purgeSQL string) []byte {
	t.Helper()
	manifest, err := json.Marshal(map[string]any{
		"arcx": 1, "name": name, "version": "1.0.0", "title": map[string]string{"en": name},
		"requires": map[string]int{"api": 1}, "permissions": []string{"db"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	files := map[string]string{
		"manifest.json":                    string(manifest),
		"migrations/racer_0001_things.sql": "CREATE TABLE racer_things (id TEXT PRIMARY KEY);",
		"purge/0001_drop.sql":              purgeSQL,
	}
	for file, body := range files {
		w, err := zw.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// An update holds the manager's lock for its whole run and swaps the new version
// in before it lets go. An uninstall that reads which version is installed before
// it takes the lock would purge the version the update replaced, and leave the new
// one's data behind. The test holds the lock the way an update does, lets the
// uninstall queue behind it, swaps the new version in, and releases.
func TestAnUninstallQueuedBehindAnUpdatePurgesTheVersionThatIsInstalled(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.manager.InstallPackage(ctx, someone,
		parsed(t, purgingPackage(t, "racer", "DROP TABLE IF EXISTS racer_old;")),
		SourceUpload, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"racer_old", "racer_new"} {
		if _, err := r.db.Exec(ctx, "CREATE TABLE "+table+" (id TEXT PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
	}
	next := &loaded{name: "racer", pkg: parsed(t, purgingPackage(t, "racer", "DROP TABLE IF EXISTS racer_new;"))}

	m := r.manager
	m.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- m.Uninstall(ctx, someone, "racer", true) }()
	// Long enough for the uninstall to reach the lock. Without the lock it has
	// read the installed version by then, and the swap below is too late for it.
	time.Sleep(100 * time.Millisecond)
	m.swapPackages(func(pkgs map[string]*loaded) { pkgs["racer"] = next })
	m.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if tableInDB(t, r, "racer_new") {
		t.Fatal("the installed version's purge did not run: its table survived the uninstall")
	}
}

func tableInDB(t *testing.T, r *rig, table string) bool {
	t.Helper()
	var n int
	if err := r.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}
