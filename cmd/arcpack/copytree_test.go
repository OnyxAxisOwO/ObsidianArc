package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// copyTree is what puts a source file into a package, so a link in the source is
// refused rather than followed: a link can name any file the build is able to
// read, and a copy of that file would ship as the plugin's own.
func TestCopyTreeRefusesLinksAndCopiesNothingOfWhatTheyName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs privileges on windows")
	}
	secret := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(secret, []byte("super-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("a manifest that is a link", func(t *testing.T) {
		src := t.TempDir()
		link(t, secret, filepath.Join(src, "manifest.json"))
		stage := filepath.Join(t.TempDir(), "stage")
		err := copyTree(filepath.Join(src, "manifest.json"), filepath.Join(stage, "manifest.json"))
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("a linked manifest was copied: %v", err)
		}
		if leaks(stage) {
			t.Fatal("the link's target was copied")
		}
	})

	t.Run("a file in web that is a link", func(t *testing.T) {
		src := t.TempDir()
		write(t, filepath.Join(src, "web", "ui.js"), "export default {};")
		link(t, secret, filepath.Join(src, "web", "banner.png"))
		stage := filepath.Join(t.TempDir(), "stage")
		if err := copyTree(filepath.Join(src, "web"), filepath.Join(stage, "web")); err == nil {
			t.Fatal("a linked file in web was copied")
		}
		if leaks(stage) {
			t.Fatal("the link's target was copied")
		}
	})

	t.Run("a directory that is a link", func(t *testing.T) {
		elsewhere := t.TempDir()
		write(t, filepath.Join(elsewhere, "0001_things.sql"), "-- super-secret")
		src := t.TempDir()
		link(t, elsewhere, filepath.Join(src, "migrations"))
		stage := filepath.Join(t.TempDir(), "stage")
		if err := copyTree(filepath.Join(src, "migrations"), filepath.Join(stage, "migrations")); err == nil {
			t.Fatal("a linked directory was copied")
		}
		if leaks(stage) {
			t.Fatal("the linked directory's files were copied")
		}
	})

	// A link to nothing was once skipped as a file that is not there; it is a
	// link, and it is refused.
	t.Run("a link to nothing", func(t *testing.T) {
		src := t.TempDir()
		link(t, filepath.Join(t.TempDir(), "gone"), filepath.Join(src, "purge"))
		if err := copyTree(filepath.Join(src, "purge"), filepath.Join(t.TempDir(), "stage", "purge")); err == nil {
			t.Fatal("a dangling link was skipped rather than refused")
		}
	})
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// leaks reports whether anything copied under dir holds the secret's bytes.
func leaks(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if body, err := os.ReadFile(p); err == nil && strings.Contains(string(body), "super-secret") {
			found = true
		}
		return nil
	})
	return found
}
