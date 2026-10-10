package arcx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Pack stores what it finds in the directory, so a link there is refused rather
// than followed: the bytes it names would be stored under the link's own name,
// and nothing in the archive would show that they came from elsewhere.
func TestPackRefusesALinkRatherThanStoringWhatItNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs privileges on windows")
	}
	secret := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(secret, []byte("super-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ at, target string }{
		"a link to a file":      {"README.md", secret},
		"a link to a directory": {"web", t.TempDir()},
		"a dangling link":       {"README.md", filepath.Join(t.TempDir(), "gone")},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			writePackTree(t, dir, goodEntries())
			if err := os.RemoveAll(filepath.Join(dir, c.at)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(c.target, filepath.Join(dir, c.at)); err != nil {
				t.Fatal(err)
			}
			if _, err := Pack(dir); err == nil || !strings.Contains(err.Error(), c.at+" is a link or special file") {
				t.Fatalf("a directory with %s packed, or refused for another reason: %v", label, err)
			}
		})
	}

	// A dot name is outside the package whatever it is, as it always was, so a
	// link under one does not stop the pack.
	t.Run("a link under a dot name", func(t *testing.T) {
		dir := t.TempDir()
		writePackTree(t, dir, goodEntries())
		if err := os.Symlink(secret, filepath.Join(dir, ".credentials")); err != nil {
			t.Fatal(err)
		}
		if _, err := Pack(dir); err != nil {
			t.Fatalf("a dotted link stopped the pack: %v", err)
		}
	})
}

// writePackTree lays entries out under dir the way a plugin's source is.
func writePackTree(t *testing.T, dir string, entries map[string]string) {
	t.Helper()
	for name, body := range entries {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
