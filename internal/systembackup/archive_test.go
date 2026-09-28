package systembackup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

var testMasterKey = []byte("automated-backup-tests-instance-key")

func openBackupTestDB(t *testing.T, path string) *database.DB {
	t.Helper()
	return openBackupDB(t, config.Database{
		Driver: "sqlite", DSN: filepath.Join(path, "arc.db"), MaxOpenConns: 4, MaxIdleConns: 2,
	})
}

func openBackupDB(t *testing.T, cfg config.Database) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		_ = db.Close()
		t.Fatalf("migrate SQLite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type sampleData struct {
	userID, providerID, modelID, routedModelID string
	conversationID, messageID, attachmentID    string
}

func insertSampleData(t *testing.T, db *database.DB, label string) sampleData {
	t.Helper()
	ctx := context.Background()
	stamp := time.Now().UnixMilli()
	d := sampleData{
		userID: "user-" + label, providerID: "provider-" + label,
		modelID: "model-" + label, routedModelID: "route-" + label,
		conversationID: "conversation-" + label, messageID: "message-" + label,
		attachmentID: "attachment-" + label,
	}
	groupID := "group-" + label
	projectID := "project-" + label
	if _, err := db.Exec(ctx, `INSERT INTO user_groups (id,name,created_at,updated_at) VALUES (?,?,?,?)`, groupID, "Group "+label, stamp, stamp); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users (id,username,username_lower,password_hash,role,group_id,status,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, d.userID, "user-"+label, "user-"+label, "hash", "super_admin", groupID, "active", stamp, stamp); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO providers (id,name,kind,base_url,api_key_enc,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?)`, d.providerID, "Provider "+label, "openai", "https://example.test/v1", []byte{0, 1, 255}, stamp, stamp); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO models (id,provider_id,model_id,display_name,created_at,updated_at)
		VALUES (?,?,?,?,?,?)`, d.modelID, d.providerID, "model", "Model "+label, stamp, stamp); err != nil {
		t.Fatalf("insert model: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO models (id,provider_id,model_id,display_name,created_at,updated_at,route_to_id)
		VALUES (?,?,?,?,?,?,?)`, d.routedModelID, d.providerID, "routed", "Routed "+label, stamp, stamp, d.modelID); err != nil {
		t.Fatalf("insert routed model: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO projects (id,user_id,name,instructions,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
		projectID, d.userID, "Project "+label, "keep this prompt", stamp, stamp); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO conversations (id,user_id,title,model_id,message_count,created_at,updated_at,mode,project_id)
		VALUES (?,?,?,?,?,?,?,?,?)`, d.conversationID, d.userID, "Transcript", d.modelID, 1, stamp, stamp, "chat", projectID); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO messages (id,conversation_id,user_id,seq,role,content,model_id,provider_id,created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, d.messageID, d.conversationID, d.userID, 0, "user", "restore me", d.modelID, d.providerID, stamp); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO attachments (id,user_id,message_id,mime,width,height,size,data,created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, d.attachmentID, d.userID, d.messageID, "application/octet-stream", 0, 0, 5, []byte{0, 1, 2, 0xfe, 0xff}, stamp); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO settings (key,value,updated_at) VALUES (?,?,?)`, "site.name", "Instance "+label, stamp); err != nil {
		t.Fatalf("insert setting: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO settings (key,value,updated_at) VALUES (?,?,?)`, schedulerLockKey, "", stamp); err != nil {
		t.Fatalf("insert lock marker: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO system_backups (id,instance_id,enabled,endpoint,bucket,access_key_id_enc,secret_access_key_enc)
		VALUES (?,?,?,?,?,?,?)`, "instance", "source-instance", true, "https://old.example", "old-bucket", []byte{1, 2}, []byte{3, 4}); err != nil {
		t.Fatalf("insert source scheduler: %v", err)
	}
	return d
}

func makeArchive(t *testing.T, db *database.DB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "instance.arcbackup")
	writeArchiveFile(t, db, path)
	return path
}

func writeArchiveFile(t *testing.T, db *database.DB, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	if err := writeArchive(context.Background(), db, file, testMasterKey, "test"); err != nil {
		_ = file.Close()
		t.Fatalf("write archive: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
}

// The row cap has to be judged against what an operator may configure, not
// just against the rows this repo's own tests write: an attachment at the
// very ceiling travels base64-encoded and would blow past a cap sized by
// eye. The relation is pinned here so neither constant can move alone.
func TestArchiveRowCapCoversTheAttachmentCeiling(t *testing.T) {
	if maxArchiveRowBytes < 2*settings.MaxAttachmentCeilingMB<<20 {
		t.Fatalf("archive row cap = %d MiB, want at least twice the %d MB attachment ceiling",
			maxArchiveRowBytes>>20, settings.MaxAttachmentCeilingMB)
	}
}

// An attachment at the very ceiling is the row the archive cap exists to
// carry: base64 inflates 64 MB to roughly 85 MiB, which the old cap
// rejected outright — every backup of such an instance failed for as long
// as the attachment lived. The round trip proves both halves accept it.
func TestArchiveRoundTripsAnAttachmentAtTheCeiling(t *testing.T) {
	source := openBackupTestDB(t, t.TempDir())
	want := insertSampleData(t, source, "ceiling")
	blob := make([]byte, settings.MaxAttachmentCeilingMB<<20)
	for i := range blob {
		blob[i] = byte(i * 7)
	}
	if _, err := source.Exec(context.Background(),
		`UPDATE attachments SET data = ?, size = ? WHERE id = ?`, blob, len(blob), want.attachmentID); err != nil {
		t.Fatalf("store ceiling attachment: %v", err)
	}

	archive := makeArchive(t, source)
	destination := openBackupTestDB(t, t.TempDir())
	if err := RestoreArchive(context.Background(), destination, archive, testMasterKey); err != nil {
		t.Fatalf("restore archive: %v", err)
	}

	var restored []byte
	if err := destination.QueryRow(context.Background(),
		`SELECT data FROM attachments WHERE id = ?`, want.attachmentID).Scan(&restored); err != nil {
		t.Fatalf("read restored attachment: %v", err)
	}
	if !bytes.Equal(restored, blob) {
		t.Fatalf("restored %d bytes, want the %d-byte attachment", len(restored), len(blob))
	}
	var size int
	if err := destination.QueryRow(context.Background(),
		`SELECT size FROM attachments WHERE id = ?`, want.attachmentID).Scan(&size); err != nil || size != len(blob) {
		t.Fatalf("restored size = %d, %v; want %d", size, err, len(blob))
	}
}

func TestArchiveRestorePreservesBinaryRowsAndSelfReferences(t *testing.T) {
	source := openBackupTestDB(t, t.TempDir())
	want := insertSampleData(t, source, "roundtrip")
	archive := makeArchive(t, source)
	destination := openBackupTestDB(t, t.TempDir())

	if err := RestoreArchive(context.Background(), destination, archive, testMasterKey); err != nil {
		t.Fatalf("restore archive: %v", err)
	}
	var routeTo string
	if err := destination.QueryRow(context.Background(), `SELECT route_to_id FROM models WHERE id = ?`, want.routedModelID).Scan(&routeTo); err != nil || routeTo != want.modelID {
		t.Fatalf("model self-reference = %q, %v; want %q", routeTo, err, want.modelID)
	}
	var attachment []byte
	if err := destination.QueryRow(context.Background(), `SELECT data FROM attachments WHERE id = ?`, want.attachmentID).Scan(&attachment); err != nil {
		t.Fatalf("read restored attachment: %v", err)
	}
	if !bytes.Equal(attachment, []byte{0, 1, 2, 0xfe, 0xff}) {
		t.Fatalf("restored attachment bytes = %v", attachment)
	}
	var projectID, conversation string
	if err := destination.QueryRow(context.Background(), `SELECT project_id FROM conversations WHERE id = ?`, want.conversationID).Scan(&projectID); err != nil || projectID != "project-roundtrip" {
		t.Fatalf("restored project link = %q, %v", projectID, err)
	}
	if err := destination.QueryRow(context.Background(), `SELECT content FROM messages WHERE id = ?`, want.messageID).Scan(&conversation); err != nil || conversation != "restore me" {
		t.Fatalf("restored message = %q, %v", conversation, err)
	}
	var value string
	if err := destination.QueryRow(context.Background(), `SELECT value FROM settings WHERE key = 'site.name'`).Scan(&value); err != nil || value != "Instance roundtrip" {
		t.Fatalf("restored setting = %q, %v", value, err)
	}
	var count int
	if err := destination.QueryRow(context.Background(), `SELECT COUNT(*) FROM settings WHERE key = ?`, schedulerLockKey).Scan(&count); err != nil || count != 0 {
		t.Fatalf("restore lock marker count = %d, %v", count, err)
	}
	if err := destination.QueryRow(context.Background(), `SELECT COUNT(*) FROM system_backups`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("restored scheduler count = %d, %v", count, err)
	}
}

func TestRestoreRejectsWrongKeyBeforeWriting(t *testing.T) {
	source := openBackupTestDB(t, t.TempDir())
	insertSampleData(t, source, "wrongkey")
	archive := makeArchive(t, source)
	destination := openBackupTestDB(t, t.TempDir())

	if err := VerifyArchiveKey(archive, []byte("wrong-instance-master-key")); err == nil {
		t.Fatal("wrong master key unexpectedly accepted")
	}
	if err := RestoreArchive(context.Background(), destination, archive, []byte("wrong-instance-master-key")); err == nil {
		t.Fatal("restore accepted the wrong master key")
	}
	var count int
	if err := destination.QueryRow(context.Background(), `SELECT COUNT(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("wrong-key restore wrote %d users, %v", count, err)
	}
}

func TestCorruptArchiveRollsBackEarlierTableInserts(t *testing.T) {
	source := openBackupTestDB(t, t.TempDir())
	insertSampleData(t, source, "corrupt")
	archive := corruptAttachmentEntry(t, makeArchive(t, source))
	destination := openBackupTestDB(t, t.TempDir())

	if err := RestoreArchive(context.Background(), destination, archive, testMasterKey); err == nil {
		t.Fatal("restore unexpectedly accepted invalid attachment bytes")
	}
	for _, table := range []string{"users", "providers", "messages", "attachments"} {
		var count int
		if err := destination.QueryRow(context.Background(), `SELECT COUNT(*) FROM "`+table+`"`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("failed restore left %d rows in %s", count, table)
		}
	}
}

func corruptAttachmentEntry(t *testing.T, original string) string {
	t.Helper()
	file, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	reader, manifest, err := readManifest(file, info.Size())
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	columns := manifestTable(manifest, "attachments").Columns
	dataColumn := -1
	for index, column := range columns {
		if column == "data" {
			dataColumn = index
		}
	}
	if dataColumn < 0 {
		_ = file.Close()
		t.Fatal("attachments.data column not found")
	}
	path := filepath.Join(t.TempDir(), "corrupt.arcbackup")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	corrupted := false
	for _, entry := range reader.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatalf("open zip entry: %v", err)
		}
		payload, readErr := io.ReadAll(input)
		closeErr := input.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read zip entry %s: %v %v", entry.Name, readErr, closeErr)
		}
		if entry.Name == "data/attachments.jsonl" {
			var cells []archiveCell
			if err := json.Unmarshal(bytes.TrimSpace(payload), &cells); err != nil {
				t.Fatal(err)
			}
			cells[dataColumn].Value = "not-valid-base64%%%"
			payload, err = json.Marshal(cells)
			if err != nil {
				t.Fatal(err)
			}
			payload = append(payload, '\n')
			corrupted = true
		}
		header := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate}
		header.SetMode(0600)
		out, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := out.Write(payload); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if !corrupted {
		t.Fatal("archive did not contain attachments table")
	}
	return path
}

func TestPostgresArchiveRestoreAndSchedulerLockWhenConfigured(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "postgres.arcbackup")
	var expected sampleData
	t.Run("source", func(t *testing.T) {
		db := openBackupDB(t, dbtest.Postgres(t, "instance backup snapshot and restore portability"))
		expected = insertSampleData(t, db, "postgres")
		writeArchiveFile(t, db, archivePath)
	})
	t.Run("restore", func(t *testing.T) {
		db := openBackupDB(t, dbtest.Postgres(t, "restore and cross-instance lease exclusion"))
		if err := RestoreArchive(context.Background(), db, archivePath, testMasterKey); err != nil {
			t.Fatalf("restore PostgreSQL archive: %v", err)
		}
		var routeTo string
		if err := db.QueryRow(context.Background(), `SELECT route_to_id FROM models WHERE id = ?`, expected.routedModelID).Scan(&routeTo); err != nil || routeTo != expected.modelID {
			t.Fatalf("PostgreSQL self-reference = %q, %v", routeTo, err)
		}
		var attachment []byte
		if err := db.QueryRow(context.Background(), `SELECT data FROM attachments WHERE id = ?`, expected.attachmentID).Scan(&attachment); err != nil {
			t.Fatalf("read Postgres attachment: %v", err)
		}
		if !bytes.Equal(attachment, []byte{0, 1, 2, 0xfe, 0xff}) {
			t.Fatalf("Postgres attachment bytes = %v", attachment)
		}
		box, err := secret.New(testMasterKey, "obsidian-arc/system-backup-credentials")
		if err != nil {
			t.Fatal(err)
		}
		stores := []*Store{NewStore(db, box), NewStore(db, box)}
		for _, store := range stores {
			if err := store.Save(context.Background(), Config{
				Enabled: true, Endpoint: "https://s3.example.test", Bucket: "arc-backups",
				Region: "us-east-1", Prefix: "arc", AccessKeyID: "access", SecretKey: "secret",
				IntervalHours: 24, RetentionDays: 7,
			}); err != nil {
				t.Fatalf("save PostgreSQL scheduler config: %v", err)
			}
		}
		var wait sync.WaitGroup
		start := make(chan struct{})
		claimed := make(chan string, 2)
		errorsOut := make(chan error, 2)
		for _, store := range stores {
			wait.Add(1)
			go func(store *Store) {
				defer wait.Done()
				<-start
				token, err := store.claim(context.Background(), time.Now(), false)
				claimed <- token
				errorsOut <- err
			}(store)
		}
		close(start)
		wait.Wait()
		close(claimed)
		close(errorsOut)
		var count int
		for token := range claimed {
			if token != "" {
				count++
			}
		}
		for err := range errorsOut {
			if err != nil && err != ErrAlreadyRunning {
				t.Fatalf("claim PostgreSQL scheduler lease: %v", err)
			}
		}
		if count != 1 {
			t.Fatalf("PostgreSQL scheduled claims = %d, want one", count)
		}
	})
}

func TestConcurrentRestoreAllowsOneArchiveAndNoMixedRows(t *testing.T) {
	firstDB := openBackupTestDB(t, t.TempDir())
	first := insertSampleData(t, firstDB, "alpha")
	firstArchive := makeArchive(t, firstDB)
	secondDB := openBackupTestDB(t, t.TempDir())
	second := insertSampleData(t, secondDB, "beta")
	secondArchive := makeArchive(t, secondDB)
	destination := openBackupTestDB(t, t.TempDir())

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, path := range []string{firstArchive, secondArchive} {
		wait.Add(1)
		go func(path string) {
			defer wait.Done()
			<-start
			results <- RestoreArchive(context.Background(), destination, path, testMasterKey)
		}(path)
	}
	close(start)
	wait.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent restores succeeded %d times, want exactly one", successes)
	}
	var users, attachments int
	if err := destination.QueryRow(context.Background(), `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := destination.QueryRow(context.Background(), `SELECT COUNT(*) FROM attachments`).Scan(&attachments); err != nil {
		t.Fatal(err)
	}
	if users != 1 || attachments != 1 {
		t.Fatalf("destination has %d users and %d attachments; expected one complete archive", users, attachments)
	}
	var winner string
	if err := destination.QueryRow(context.Background(), `SELECT id FROM users`).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	if winner != first.userID && winner != second.userID {
		t.Fatalf("unexpected winner user %q", winner)
	}
}

func TestStoreClaimsScheduledRunOnceAcrossInstances(t *testing.T) {
	db := openBackupTestDB(t, t.TempDir())
	box, err := secret.New(testMasterKey, "obsidian-arc/system-backup-credentials")
	if err != nil {
		t.Fatal(err)
	}
	stores := []*Store{NewStore(db, box), NewStore(db, box)}
	const plainAccess = "BACKUP_ACCESS_PLAINTEXT_MARKER"
	const plainSecret = "BACKUP_SECRET_PLAINTEXT_MARKER"
	for _, store := range stores {
		if err := store.Save(context.Background(), Config{
			Enabled: true, Endpoint: "https://s3.example.test", Bucket: "arc-backups",
			Region: "us-east-1", Prefix: "arc", AccessKeyID: plainAccess, SecretKey: plainSecret,
			IntervalHours: 24, RetentionDays: 7,
		}); err != nil {
			t.Fatalf("save config: %v", err)
		}
	}
	now := time.Now()
	start := make(chan struct{})
	tokens := make(chan string, len(stores))
	errorsOut := make(chan error, len(stores))
	var wait sync.WaitGroup
	for _, store := range stores {
		wait.Add(1)
		go func(store *Store) {
			defer wait.Done()
			<-start
			token, err := store.claim(context.Background(), now, false)
			tokens <- token
			errorsOut <- err
		}(store)
	}
	close(start)
	wait.Wait()
	close(tokens)
	close(errorsOut)
	var claimed int
	for token := range tokens {
		if token != "" {
			claimed++
		}
	}
	for err := range errorsOut {
		if err != nil && err != ErrAlreadyRunning {
			t.Fatalf("claim scheduled backup: %v", err)
		}
	}
	if claimed != 1 {
		t.Fatalf("scheduled claims = %d, want one", claimed)
	}
	status, err := stores[0].Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{plainAccess, plainSecret} {
		if bytes.Contains(encoded, []byte(marker)) {
			t.Fatalf("status leaked a storage credential: %s", encoded)
		}
	}
}

func TestConcurrentCredentialRotationKeepsBothNewValues(t *testing.T) {
	db := openBackupTestDB(t, t.TempDir())
	box, err := secret.New(testMasterKey, "obsidian-arc/system-backup-credentials")
	if err != nil {
		t.Fatal(err)
	}
	stores := []*Store{NewStore(db, box), NewStore(db, box)}
	base := Config{
		Enabled: true, Endpoint: "https://s3.example.test", Bucket: "arc-backups",
		Region: "us-east-1", Prefix: "arc", AccessKeyID: "old-access", SecretKey: "old-secret",
		IntervalHours: 24, RetentionDays: 7,
	}
	if err := stores[0].Save(context.Background(), base); err != nil {
		t.Fatalf("save initial configuration: %v", err)
	}
	accessUpdate := base
	accessUpdate.AccessKeyID = "new-access"
	accessUpdate.SecretKey = ""
	secretUpdate := base
	secretUpdate.AccessKeyID = ""
	secretUpdate.SecretKey = "new-secret"
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var wait sync.WaitGroup
	for i, update := range []Config{accessUpdate, secretUpdate} {
		wait.Add(1)
		go func(store *Store, update Config) {
			defer wait.Done()
			<-start
			errorsOut <- store.Save(context.Background(), update)
		}(stores[i], update)
	}
	close(start)
	wait.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("save credential update: %v", err)
		}
	}
	loaded, err := stores[0].Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessKeyID != "new-access" || loaded.SecretKey != "new-secret" {
		t.Fatalf("concurrent credential saves left access=%q secret=%q", loaded.AccessKeyID, loaded.SecretKey)
	}
	var sealedAccess, sealedSecret []byte
	if err := db.QueryRow(context.Background(), `SELECT access_key_id_enc, secret_access_key_enc FROM system_backups WHERE id = ?`, singletonID).Scan(&sealedAccess, &sealedSecret); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealedAccess, []byte("new-access")) || bytes.Contains(sealedSecret, []byte("new-secret")) {
		t.Fatal("database credential columns contain plaintext")
	}
}
