package systembackup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

// PostgreSQL refuses the whole UPDATE for a NUL byte or invalid UTF-8, and
// appendLog's callers discard that error, so a line carrying either would be
// lost without a trace. SQLite accepts both, which is why this must be checked
// on the SQLite path: the cleaning has to happen in the store, not in the
// database.
func TestAppendLogStoresACleanedLine(t *testing.T) {
	db := openBackupTestDB(t, t.TempDir())
	box, err := secret.New(testMasterKey, "obsidian-arc/system-backup-credentials")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, box)
	ctx := context.Background()
	if err := store.Save(ctx, Config{
		Enabled: true, Endpoint: "https://s3.example.test", Bucket: "arc-backups",
		Region: "us-east-1", Prefix: "arc", AccessKeyID: "access", SecretKey: "secret",
		IntervalHours: 24, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	token, err := store.claim(ctx, time.Now(), true)
	if err != nil || token == "" {
		t.Fatalf("claim: %v (token: %q)", err, token)
	}
	if err := store.appendLog(ctx, token, "Staging\x00 snapshot\xff done"); err != nil {
		t.Fatalf("appendLog: %v", err)
	}
	status, err := store.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(status.LastLog, "Staging snapshot� done\n") {
		t.Errorf("last_log does not hold the cleaned line: %q", status.LastLog)
	}
	if strings.Contains(status.LastLog, "\x00") {
		t.Errorf("last_log kept a NUL byte: %q", status.LastLog)
	}
}
