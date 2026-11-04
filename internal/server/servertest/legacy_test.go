package servertest

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
)

// legacyProbe is a plugin with one table, registered for this package's tests
// only. The core ships no plugin, and NewLegacy has nothing to adopt without
// one: its whole purpose is a plugin whose migrations ran before plugins could
// be switched.
type legacyProbe struct{}

func (legacyProbe) Name() string              { return "legacyprobe" }
func (legacyProbe) Manifest() plugin.Manifest { return plugin.Manifest{Version: "1.0.0"} }
func (legacyProbe) Setup(*plugin.Host) error  { return nil }

func (legacyProbe) Migrations() fs.FS {
	return fstest.MapFS{
		"legacyprobe_0001_items.sql": {Data: []byte(`CREATE TABLE legacyprobe_items (id TEXT PRIMARY KEY)`)},
	}
}

func init() { plugin.Register(legacyProbe{}) }

// An upgrade meets a database where a plugin's tables already exist and nothing
// records its state. The boot has to decide that state itself. Adopting it as
// enabled is what keeps the plugin's data reachable after the upgrade, and a
// boot that left it unrecorded would show the plugin as never installed over
// tables it owns.
func TestNewLegacyAdoptsAPluginWhoseMigrationsAlreadyRan(t *testing.T) {
	in := NewLegacy(t, nil)
	ctx := context.Background()

	var state string
	if err := in.DB.QueryRow(ctx, `SELECT state FROM plugin_installs WHERE name = ?`, "legacyprobe").Scan(&state); err != nil {
		t.Fatalf("the boot recorded no state for the plugin: %v", err)
	}
	if state != "enabled" {
		t.Errorf("state = %q, want enabled", state)
	}

	var rows int
	if err := in.DB.QueryRow(ctx, `SELECT COUNT(*) FROM legacyprobe_items`).Scan(&rows); err != nil {
		t.Fatalf("the plugin's table is not there after the boot: %v", err)
	}
}
