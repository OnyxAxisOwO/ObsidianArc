package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// The gate is the only thing standing between a restore and a freshly
// generated key, so each way of having or lacking the original key is pinned.
// The lacking case also checks that nothing was written: a gate that created a
// key would pass the refusal and still do the damage.
func TestRestoreRequiresTheOriginalKeyAndCreatesNothing(t *testing.T) {
	t.Run("an environment key is enough", func(t *testing.T) {
		t.Setenv("OBSIDIAN_SECRET_KEY", "an-environment-key-value")
		t.Setenv("OBSIDIAN_DATA_DIR", t.TempDir())
		if err := requireExistingRestoreKey(); err != nil {
			t.Fatalf("an environment key was refused: %v", err)
		}
	})

	t.Run("a blank environment key does not count", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("OBSIDIAN_SECRET_KEY", "   ")
		t.Setenv("OBSIDIAN_DATA_DIR", dir)
		if err := requireExistingRestoreKey(); err == nil {
			t.Fatal("a blank environment key was accepted with no key file")
		}
		assertNoSecretFile(t, dir)
	})

	t.Run("a persisted key is enough", func(t *testing.T) {
		dir := t.TempDir()
		writeSecretFile(t, dir, strings.Repeat("k", config.MinSecretLen)+"\n")
		t.Setenv("OBSIDIAN_SECRET_KEY", "")
		t.Setenv("OBSIDIAN_DATA_DIR", dir)
		if err := requireExistingRestoreKey(); err != nil {
			t.Fatalf("a persisted key was refused: %v", err)
		}
	})

	t.Run("a persisted key below the floor is refused", func(t *testing.T) {
		dir := t.TempDir()
		writeSecretFile(t, dir, strings.Repeat("k", config.MinSecretLen-1))
		t.Setenv("OBSIDIAN_SECRET_KEY", "")
		t.Setenv("OBSIDIAN_DATA_DIR", dir)
		if err := requireExistingRestoreKey(); err == nil {
			t.Fatal("a key under the length floor was accepted")
		}
	})

	t.Run("no key at all is refused and nothing is created", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("OBSIDIAN_SECRET_KEY", "")
		t.Setenv("OBSIDIAN_DATA_DIR", dir)
		if err := requireExistingRestoreKey(); err == nil {
			t.Fatal("a restore with no key was allowed to continue")
		}
		assertNoSecretFile(t, dir)
	})
}

func writeSecretFile(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, config.SecretKeyFile), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNoSecretFile(t *testing.T, dir string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, config.SecretKeyFile))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the restore gate left a key file behind: stat error %v", err)
	}
}
