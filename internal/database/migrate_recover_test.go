package database

import (
	"errors"
	"strings"
	"testing"
)

// A lost first-boot race is recovered only when the table can be read back.
// If the readback fails too, the boot error has to carry that failure as well:
// the collision alone would send the operator looking at the wrong cause.
func TestRecoverMigrationsTableNeedsTheReadback(t *testing.T) {
	createErr := errors.New("duplicate key value violates unique constraint pg_type_typname_nsp_index")

	if err := recoverMigrationsTable(createErr, func() error { return nil }); err != nil {
		t.Fatalf("a readable table after a lost race = %v, want nil", err)
	}

	probeErr := errors.New("connection reset by peer")
	err := recoverMigrationsTable(createErr, func() error { return probeErr })
	if err == nil {
		t.Fatal("a failed readback returned nil; the table was never shown to exist")
	}
	if !errors.Is(err, createErr) {
		t.Errorf("error %q does not wrap the create error", err)
	}
	if !strings.Contains(err.Error(), probeErr.Error()) {
		t.Errorf("error %q does not mention the readback error %q", err, probeErr)
	}
}
