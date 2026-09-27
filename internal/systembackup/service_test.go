package systembackup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPruneExpiredDeletesOnlyOldGeneratedObjectsOwnedByInstance(t *testing.T) {
	instanceID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	cfg := Config{
		Endpoint: "", Bucket: "qa-backups", Region: "auto", Prefix: "operator/backups",
		AccessKeyID: "test-access", SecretKey: "test-secret", RetentionDays: 7,
	}
	old := backupObjectKey(cfg, instanceID, now.Add(-9*24*time.Hour))
	fresh := backupObjectKey(cfg, instanceID, now.Add(-time.Hour))
	foreign := backupObjectKey(cfg, "01ARZ3NDEKTSV4RRFFQ69G5FAW", now.Add(-30*24*time.Hour))
	malformed := fmt.Sprintf("%s/%s/instance-not-a-timestamp.arcbackup", cfg.Prefix, instanceID)
	var deleted []string
	expectedPrefix := fmt.Sprintf("%s/%s/", cfg.Prefix, instanceID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if got := r.URL.Query().Get("prefix"); got != expectedPrefix {
				t.Errorf("list prefix = %q, want %q", got, expectedPrefix)
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprintf(w, `<ListBucketResult><Contents><Key>%s</Key></Contents><Contents><Key>%s</Key></Contents><Contents><Key>%s</Key></Contents><Contents><Key>%s</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`, old, fresh, foreign, malformed)
		case http.MethodDelete:
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/"+cfg.Bucket+"/"))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	cfg.Endpoint = server.URL
	client, err := NewS3Client(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneExpired(context.Background(), client, cfg, instanceID, now); err != nil {
		t.Fatalf("prune expired objects: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != old {
		t.Fatalf("deleted keys = %#v, want only expired key %q", deleted, old)
	}
}
