package consolessh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// loadOrCreateHostKey returns the console's SSH identity, generating and
// persisting one on first start. Idempotent: a second call against the same
// path reads back exactly what the first one wrote, which is what lets
// `ssh admin@host` show the same fingerprint across restarts instead of
// tripping every client's known-hosts check on every deploy.
//
// The key is ed25519 for the same reason internal/auth prefers Argon2id over
// rolling its own: one well-reviewed primitive, no size/curve choice to get
// wrong, and it is the format golang.org/x/crypto/ssh round-trips natively
// through MarshalPrivateKey/ParsePrivateKey with no extra encoding step.
func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	if path == "" {
		return nil, errors.New("consolessh: host key path is empty")
	}

	signer, found, err := readHostKey(path)
	if err != nil || found {
		return signer, err
	}

	// Mirrors internal/config's secret-file pattern: 0700 directory, 0600
	// file. A host key under a custom OBSIDIAN_SSH_HOST_KEY path should not
	// depend on its parent directory already existing.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("consolessh: create host key directory: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("consolessh: generate host key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "obsidian-arc console host key")
	if err != nil {
		return nil, fmt.Errorf("consolessh: marshal host key: %w", err)
	}
	encoded := pem.EncodeToMemory(block)

	// Two starts on one fresh data directory each generate a key. If both simply
	// renamed into place, the one renamed first would sign with an identity no
	// longer on disk, and the next restart would show a different fingerprint.
	created, err := createHostKey(path, encoded)
	if err != nil {
		return nil, fmt.Errorf("consolessh: write host key %s: %w", path, err)
	}
	if !created {
		// The other start won. Sign with the key it wrote, which is the one on disk.
		winner, found, err := readHostKey(path)
		if err != nil {
			return nil, err
		}
		if found {
			return winner, nil
		}
		// What is there is an empty leftover, which readHostKey treats as absent.
		if err := writeFileAtomic(path, encoded); err != nil {
			return nil, fmt.Errorf("consolessh: write host key %s: %w", path, err)
		}
		// A start holding the same leftover may have renamed its own key over
		// ours. Sign with what the disk holds now. This only narrows the race to
		// the gap between our rename and this read: nothing serializes the two
		// replacements, so a read that lands before the other rename still signs
		// with a key the disk will not keep.
		winner, found, err = readHostKey(path)
		if err != nil {
			return nil, err
		}
		if found {
			return winner, nil
		}
	}

	// Round-tripping through ParsePrivateKey rather than building the
	// signer straight from priv means a corrupt write is caught here, at
	// startup, instead of surfacing as a handshake failure to the first
	// person who tries to connect.
	signer, err = ssh.ParsePrivateKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("consolessh: parse newly written host key: %w", err)
	}
	return signer, nil
}

// readHostKey returns the key stored at path. found is false when there is no
// file or it is empty: a zero-length file is the leftover of an interrupted
// write from an older build, and no client can have trusted it, so refusing to
// boot over it would take the HTTP side down too.
func readHostKey(path string) (ssh.Signer, bool, error) {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil && len(existing) > 0:
		signer, parseErr := ssh.ParsePrivateKey(existing)
		if parseErr != nil {
			return nil, false, fmt.Errorf("consolessh: parse host key %s: %w", path, parseErr)
		}
		return signer, true, nil
	case err != nil && !os.IsNotExist(err):
		return nil, false, fmt.Errorf("consolessh: read host key %s: %w", path, err)
	}
	return nil, false, nil
}

// createHostKey stores data at path only if nothing is there yet, and reports
// whether it did. The bytes are staged in a temp file and hard-linked into
// place: the link publishes the whole key or fails because the name is taken,
// so a crash cannot leave a partial key and two starts cannot both win. A
// filesystem without hard links falls back to a rename, which keeps the file
// whole but cannot tell two starts apart.
func createHostKey(path string, data []byte) (bool, error) {
	staged, err := stageFile(path, data)
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(staged) }()

	err = os.Link(staged, path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrExist):
		return false, nil
	}
	if err := os.Rename(staged, path); err != nil {
		return false, err
	}
	return true, nil
}

// writeFileAtomic replaces path with data through a temp file in the same
// directory, so the rename is atomic on one filesystem. A plain WriteFile
// truncates first: a crash or a full disk in between leaves a partial PEM that
// stops the server from booting until someone deletes it, and deleting it
// changes the fingerprint every client has trusted.
func writeFileAtomic(path string, data []byte) error {
	staged, err := stageFile(path, data)
	if err != nil {
		return err
	}
	// After a successful rename the temp name no longer exists, so this only
	// does work on the failure paths.
	defer func() { _ = os.Remove(staged) }()
	return os.Rename(staged, path)
}

// syncStaged is File.Sync for stageFile. It is a variable so a test can fail the
// sync and check that the temp file is removed.
var syncStaged = func(f *os.File) error { return f.Sync() }

// stageFile writes data, synced, to a new temp file beside path and returns its
// name. The temp file holds key material. A failure that is returned removes
// it, but a crash or kill between CreateTemp and the link or rename leaves a
// 0600 copy of the key in the data directory until someone deletes it. Nothing
// sweeps for those at startup: a concurrent start's staged file could be
// removed before its os.Link finds it by name, and that start would then fail.
func stageFile(path string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	staged := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(staged)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := syncStaged(tmp); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	done = true
	return staged, nil
}

// HostKeyFingerprint loads or creates the host key at path and returns its
// fingerprint without standing a server up.
//
// It exists for one ordering problem in the wiring: `help ssh` prints the
// fingerprint, so the console engine needs it — but the SSH server needs the
// console engine, and the two cannot both be built second. Loading the key is
// idempotent, so the wiring reads the fingerprint here first and the server
// reads the same key back a moment later.
func HostKeyFingerprint(path string) (string, error) {
	signer, err := loadOrCreateHostKey(path)
	if err != nil {
		return "", err
	}
	return fingerprint(signer), nil
}

// fingerprint renders a host key the same way OpenSSH's own `ssh-keygen -lf`
// does, which is what an administrator compares against `help ssh`'s output
// before trusting a new console.
func fingerprint(signer ssh.Signer) string {
	return ssh.FingerprintSHA256(signer.PublicKey())
}
