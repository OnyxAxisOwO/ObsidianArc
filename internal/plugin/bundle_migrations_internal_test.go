package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
)

// A bundle that still holds the archive of a plugin the build now compiles in
// is offered nothing for it. plugin.Migrations already carries that plugin's
// migrations, and the restore would find each version defined twice.
func TestBundledMigrationsSkipsTheArchiveOfACompiledInPlugin(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.arcx"), pkgtest.Demo(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := BundledMigrations(dir); len(got) != 1 {
		t.Fatalf("a plugin that is not compiled in is offered %d sources; want its own", len(got))
	}

	Register(fake{name: "demo", setup: noSetup})
	if got := BundledMigrations(dir); len(got) != 0 {
		t.Fatalf("a compiled-in plugin's archive is offered again: %d sources", len(got))
	}
}
