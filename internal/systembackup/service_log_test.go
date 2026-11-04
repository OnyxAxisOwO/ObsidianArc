package systembackup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The archive is a zip. Only the values the instance key sealed in the
// database stay sealed inside it, so a run log that says "encrypted" tells an
// operator the bucket holds nothing readable when it holds a database.
func TestTheRunLogDoesNotClaimTheArchiveIsEncrypted(t *testing.T) {
	db := openBackupTestDB(t, t.TempDir())
	svc, err := NewService(db, testMasterKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	cfg := Config{
		Type: StorageTypeS3, Enabled: true, Endpoint: server.URL, Bucket: "qa-backups", Region: "auto",
		Prefix: "operator/backups", AccessKeyID: "test-access", SecretKey: "test-secret",
		IntervalHours: 24, RetentionHours: 168,
	}
	ctx := context.Background()
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
		t.Fatalf("the backup did not complete: %q %s\n%s", status.LastStatus, status.LastError, status.LastLog)
	}
	if !strings.Contains(status.LastLog, "Snapshot archived") {
		t.Fatalf("the run log never says the snapshot was archived:\n%s", status.LastLog)
	}
	if strings.Contains(strings.ToLower(status.LastLog), "encrypt") {
		t.Fatalf("the run log calls a plain zip encrypted:\n%s", status.LastLog)
	}
}
