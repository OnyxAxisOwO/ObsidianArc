package consolessh

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A host key left as a zero-length file by an interrupted write must not stop
// the server from booting. Before the fix ParsePrivateKey rejected the empty
// file and the error reached server startup.
func TestZeroLengthHostKeyFileIsReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh_host_ed25519_key")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("seed empty key: %v", err)
	}

	signer, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("load over an empty file: %v", err)
	}

	again, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("reload after replacement: %v", err)
	}
	if fingerprint(signer) != fingerprint(again) {
		t.Fatal("the replaced key does not read back as the one that was written")
	}

	// The atomic write goes through a temp file beside the key; none may be
	// left behind once the rename has succeeded.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "ssh_host_ed25519_key" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only the host key", names)
	}
}

// The staged temp file holds key material, so a failure after the write must not
// leave it in the data directory.
func TestStagedKeyIsRemovedWhenItsSyncFails(t *testing.T) {
	previous := syncStaged
	syncStaged = func(*os.File) error { return errors.New("disk full") }
	t.Cleanup(func() { syncStaged = previous })

	dir := t.TempDir()
	path := filepath.Join(dir, "ssh_host_ed25519_key")
	if _, err := stageFile(path, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n")); err == nil {
		t.Fatal("stageFile succeeded although its sync failed")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a failed stage left %d file(s) in the data directory", len(entries))
	}
}
