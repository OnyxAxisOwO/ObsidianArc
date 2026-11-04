package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// declaredReadOnly is every core setting a settings export carries and an
// import skips, each with the reason it is read-only. A key the export carries
// and the import refuses has to be listed here, or the export is not
// re-importable as it stands and the refusal is only a count.
var declaredReadOnly = map[string]string{
	// /api/admin/checkin owns these and validates them; a write through the
	// generic settings route would skip that validation.
	settings.CheckinEnabled:  "owned by /api/admin/checkin",
	settings.CheckinTimezone: "owned by /api/admin/checkin",
	settings.CheckinConfig:   "owned by /api/admin/checkin",
	// Runtime bookkeeping the purge and the janitor write; no administrator sets it.
	settings.AttachmentPurgeLast: "written by the purge and the janitor",
}

// A key the founder can read must be one the founder can write back, or be
// declared read-only with a reason. chat.allow_archive was exported and never
// writable, so an operator could see the switch and never turn it off.
func TestEveryExportedSettingIsWritableOrDeclaredReadOnly(t *testing.T) {
	h := &Handlers{settings: settings.New(nil)}
	exported := h.visibleSettings(user.User{Role: user.RoleSuperAdmin})
	if len(exported) == 0 {
		t.Fatal("the export carries no settings to check")
	}

	for key := range exported {
		if h.writable(key) {
			continue
		}
		if _, declared := declaredReadOnly[key]; declared {
			continue
		}
		t.Errorf("%q is exported but not writable, and not declared read-only", key)
	}

	for key := range declaredReadOnly {
		if _, carried := exported[key]; !carried {
			t.Errorf("%q is declared read-only but the export does not carry it", key)
		}
		if h.writable(key) {
			t.Errorf("%q is declared read-only but is writable", key)
		}
	}
}

// The export check above covers what the screen shows. This one covers what the
// server acts on, wherever under internal/ the read happens: a setting read
// anywhere and refused by the settings route is one an administrator can never
// change, which is how archiving was turned on for good and could not be turned
// off.
func TestEverySettingTheServerReadsIsWritableOrDeclaredReadOnly(t *testing.T) {
	constants := map[string]string{}
	source, err := os.ReadFile("../settings/settings.go")
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	for _, match := range regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*"([^"]+)"\s*$`).FindAllStringSubmatch(string(source), -1) {
		constants[match[1]] = match[2]
	}

	h := &Handlers{settings: settings.New(nil)}
	reads := regexp.MustCompile(`\.(?:Bool|Int|Get)\(settings\.(\w+)`)
	// Every package under internal/, not a list of the files that read settings
	// today: a new reader in a new file is exactly what this has to catch.
	var files []string
	if err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	found := 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range reads.FindAllStringSubmatch(string(body), -1) {
			found++
			key, known := constants[match[1]]
			if !known {
				t.Fatalf("%s reads settings.%s, which is not a constant in settings.go; the scanner has drifted", file, match[1])
			}
			if h.writable(key) {
				continue
			}
			if _, declared := declaredReadOnly[key]; declared {
				continue
			}
			t.Errorf("%s reads %q, which the settings route refuses and which is not declared read-only", file, key)
		}
	}
	if found == 0 {
		t.Fatal("found no settings reads; the scanner has drifted from the source")
	}
}
