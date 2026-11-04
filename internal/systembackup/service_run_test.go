package systembackup

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func retentionTestConfig(endpoint string) Config {
	return Config{
		Type: StorageTypeS3, Enabled: true, Endpoint: endpoint, Bucket: "qa-backups", Region: "auto",
		Prefix: "operator/backups", AccessKeyID: "test-access", SecretKey: "test-secret",
		IntervalHours: 24, RetentionHours: 168,
	}
}

// The archive is stored before retention runs. A listing that fails afterwards
// must be a warning on a run that succeeded, not a failed backup: the operator
// would otherwise see a failure for an archive that exists.
func TestAFailedRetentionPassDoesNotFailARunThatUploaded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			http.Error(w, "listing unavailable", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	db := openBackupTestDB(t, t.TempDir())
	svc, err := NewService(db, testMasterKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := retentionTestConfig(server.URL)
	ctx := t.Context()
	if err := svc.Save(ctx, cfg); err != nil {
		t.Fatalf("save the storage: %v", err)
	}
	token, err := svc.store.claim(ctx, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	svc.runClaimed(ctx, token, cfg)

	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastStatus != "success" {
		t.Fatalf("an uploaded run was recorded as %q: %s\n%s", status.LastStatus, status.LastError, status.LastLog)
	}
	if !strings.Contains(status.LastLog, "Retention policy could not be applied") {
		t.Fatalf("the failed retention pass is not in the run log:\n%s", status.LastLog)
	}
}

// A run that times out or is cancelled is the run whose reason matters most,
// and the line that says why was being written on the run's own dead context,
// so it was silently dropped. The status row still got the error.
func TestTheFailureReasonIsLoggedWhenTheRunContextIsDead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	db := openBackupTestDB(t, t.TempDir())
	svc, err := NewService(db, testMasterKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := retentionTestConfig(server.URL)
	ctx := t.Context()
	if err := svc.Save(ctx, cfg); err != nil {
		t.Fatalf("save the storage: %v", err)
	}
	token, err := svc.store.claim(ctx, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}

	dead, cancel := context.WithCancel(ctx)
	cancel()
	svc.runClaimed(dead, token, cfg)

	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastStatus != "error" {
		t.Fatalf("a run on a dead context was recorded as %q", status.LastStatus)
	}
	if !strings.Contains(status.LastLog, "Backup failed") {
		t.Fatalf("the reason for the failed run was not logged:\n%s", status.LastLog)
	}
}
