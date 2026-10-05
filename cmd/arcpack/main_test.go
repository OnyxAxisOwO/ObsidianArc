package main

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A deploy decides whether a bundled package changed by its hash, so the same
// source has to pack to the same bytes wherever it was built: without
// -trimpath the checkout's path is inside the WebAssembly, and every machine
// (or every CI workspace) would look like a new version of every plugin.
func TestTheSameSourceBuildsTheSamePackageFromAnyDirectory(t *testing.T) {
	var sums [2][32]byte
	for i, dir := range []string{"a", "a-much-longer-checkout-path"} {
		root := filepath.Join(t.TempDir(), dir)
		if err := copyTree(filepath.Join("..", "..", "sdk"), filepath.Join(root, "sdk")); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(root, "demo.arcx")
		if err := build([]string{filepath.Join(root, "sdk", "examples", "demo"), "-o", out}); err != nil {
			t.Fatalf("build in %s: %v", dir, err)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		sums[i] = sha256.Sum256(raw)
	}
	if sums[0] != sums[1] {
		t.Error("the same source packed differently from two directories")
	}
}

// A plugin packed inside a repository must not change when only the
// repository's commit does. Go stamps the commit into the binary unless told
// not to, and a package that differs on every commit is reinstalled — and its
// WebAssembly compiled — at every boot, which is seconds of an unreachable
// server per plugin on every deploy.
func TestACommitInTheEnclosingRepositoryDoesNotChangeThePackage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	if err := copyTree(filepath.Join("..", "..", "sdk"), filepath.Join(root, "sdk")); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")

	var sums [2][32]byte
	for i := range sums {
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", "commit")
		out := filepath.Join(t.TempDir(), "demo.arcx")
		if err := build([]string{filepath.Join(root, "sdk", "examples", "demo"), "-o", out}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		sums[i] = sha256.Sum256(raw)
	}
	if sums[0] != sums[1] {
		t.Error("the same plugin packed differently after a commit in the repository around it")
	}
}
