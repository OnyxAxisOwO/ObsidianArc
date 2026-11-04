//go:build unix

package main

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A named pipe is neither a file nor a directory, and reading one waits for a
// writer that may never come, so copyTree refuses it before it reads.
func TestCopyTreeRefusesAPipeRatherThanWaitingOnIt(t *testing.T) {
	src := t.TempDir()
	pipe := filepath.Join(src, "0001_things.sql")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- copyTree(pipe, filepath.Join(t.TempDir(), "stage", "0001_things.sql"))
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a named pipe was copied")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("copyTree waited on a named pipe")
	}
}
