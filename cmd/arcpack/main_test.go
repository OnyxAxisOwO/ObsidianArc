package main

import (
	"crypto/sha256"
	"os"
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
