package consolessh

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A link that fails for a reason other than the name being taken, here a full
// disk, falls back to a rename. That is only safe into an empty name: a key
// another start published in the meantime must be kept, not replaced.
func TestCreateHostKeyKeepsAKeyAnotherStartPublishedWhenTheLinkFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
	previous := linkHostKey
	t.Cleanup(func() { linkHostKey = previous })
	linkHostKey = func(oldname, newname string) error {
		if err := os.WriteFile(newname, []byte("the other start's key"), 0o600); err != nil {
			t.Fatalf("publish the other start's key: %v", err)
		}
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errors.New("no space left on device")}
	}

	created, err := createHostKey(path, []byte("our key"))
	if err != nil {
		t.Fatalf("createHostKey: %v", err)
	}
	if created {
		t.Fatal("createHostKey reported that it stored the key, but the rename replaced the other start's key")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the host key: %v", err)
	}
	if string(got) != "the other start's key" {
		t.Fatalf("the key on disk is %q, want the other start's key", got)
	}
}

// Where the name is still empty, a link that cannot be made falls back to the
// rename, which is how a filesystem without hard links stores its key.
func TestCreateHostKeyFallsBackToARenameIntoAnEmptyName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
	previous := linkHostKey
	t.Cleanup(func() { linkHostKey = previous })
	linkHostKey = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errors.New("hard links are not supported")}
	}

	created, err := createHostKey(path, []byte("our key"))
	if err != nil {
		t.Fatalf("createHostKey: %v", err)
	}
	if !created {
		t.Fatal("createHostKey did not store the key into an empty name")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the host key: %v", err)
	}
	if string(got) != "our key" {
		t.Fatalf("the key on disk is %q, want our key", got)
	}
}
