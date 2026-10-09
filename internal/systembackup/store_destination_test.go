package systembackup

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func newDestinationStore(t *testing.T) *Store {
	t.Helper()
	db := openBackupTestDB(t, t.TempDir())
	box, err := secret.New(testMasterKey, "obsidian-arc/system-backup-credentials")
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db, box)
}

func savedS3Store(t *testing.T) *Store {
	t.Helper()
	store := newDestinationStore(t)
	if err := store.Save(context.Background(), Config{
		Type: StorageTypeS3, Enabled: true, Endpoint: "https://s3.example.test", Bucket: "arc-backups",
		Region: "us-east-1", Prefix: "arc", AccessKeyID: "saved-access", SecretKey: "saved-secret",
		IntervalHours: 24, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("save the S3 storage: %v", err)
	}
	return store
}

func savedWebDAVStore(t *testing.T) *Store {
	t.Helper()
	store := newDestinationStore(t)
	if err := store.Save(context.Background(), Config{
		Type: StorageTypeWebDAV, Enabled: true, WebDAVURL: "https://dav.example.test/backups",
		WebDAVUsername: "arc", WebDAVPassword: "saved-password", IntervalHours: 24, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("save the WebDAV storage: %v", err)
	}
	return store
}

func TestMovingS3DestinationWithoutTheKeysIsRefused(t *testing.T) {
	for _, move := range []struct {
		name, endpoint, bucket, region string
	}{
		{"endpoint", "https://attacker.example.test", "arc-backups", "us-east-1"},
		{"bucket", "https://s3.example.test", "attacker-bucket", "us-east-1"},
		{"region", "https://s3.example.test", "arc-backups", "eu-west-1"},
	} {
		t.Run(move.name, func(t *testing.T) {
			store := savedS3Store(t)
			ctx := context.Background()
			err := store.Save(ctx, Config{
				Type: StorageTypeS3, Enabled: true, Endpoint: move.endpoint, Bucket: move.bucket,
				Region: move.region, Prefix: "arc", IntervalHours: 24, RetentionDays: 7,
			})
			if !errors.Is(err, ErrCredentialsNeededForMove) {
				t.Fatalf("moving the %s with blank keys = %v, want ErrCredentialsNeededForMove", move.name, err)
			}
			loaded, err := store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Endpoint != "https://s3.example.test" || loaded.Bucket != "arc-backups" || loaded.Region != "us-east-1" {
				t.Fatalf("refused move still changed the destination: endpoint=%q bucket=%q region=%q",
					loaded.Endpoint, loaded.Bucket, loaded.Region)
			}
			if loaded.AccessKeyID != "saved-access" || loaded.SecretKey != "saved-secret" {
				t.Fatalf("refused move changed the saved keys: access=%q secret=%q", loaded.AccessKeyID, loaded.SecretKey)
			}
		})
	}
}

func TestMovingS3DestinationWithTheKeysSavesThem(t *testing.T) {
	store := savedS3Store(t)
	ctx := context.Background()
	if err := store.Save(ctx, Config{
		Type: StorageTypeS3, Enabled: true, Endpoint: "https://new-s3.example.test", Bucket: "arc-backups",
		Region: "us-east-1", Prefix: "arc", AccessKeyID: "new-access", SecretKey: "new-secret",
		IntervalHours: 24, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("move the storage with new keys: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Endpoint != "https://new-s3.example.test" || loaded.AccessKeyID != "new-access" || loaded.SecretKey != "new-secret" {
		t.Fatalf("moved storage = endpoint %q access %q secret %q", loaded.Endpoint, loaded.AccessKeyID, loaded.SecretKey)
	}
}

func TestMovingS3DestinationNeedsEveryStoredKeyTyped(t *testing.T) {
	// The access key ID is sealed like the secret, so each stored value has to be
	// typed again for a new destination; retyping only one of them is refused.
	for _, partial := range []struct {
		name, access, secret string
	}{
		{"secret only", "", "new-secret"},
		{"access key only", "new-access", ""},
	} {
		t.Run(partial.name, func(t *testing.T) {
			store := savedS3Store(t)
			err := store.Save(context.Background(), Config{
				Type: StorageTypeS3, Enabled: true, Endpoint: "https://attacker.example.test", Bucket: "arc-backups",
				Region: "us-east-1", Prefix: "arc", AccessKeyID: partial.access, SecretKey: partial.secret,
				IntervalHours: 24, RetentionDays: 7,
			})
			if !errors.Is(err, ErrCredentialsNeededForMove) {
				t.Fatalf("moving with only the %s = %v, want ErrCredentialsNeededForMove", partial.name, err)
			}
		})
	}
}

func TestSavingTheSameS3DestinationKeepsTheKeys(t *testing.T) {
	store := savedS3Store(t)
	ctx := context.Background()
	// Whitespace and a blank region are normalised before the comparison, so a
	// client that sends them back does not count as having moved the storage.
	if err := store.Save(ctx, Config{
		Type: StorageTypeS3, Enabled: true, Endpoint: " https://s3.example.test ", Bucket: "arc-backups",
		Region: "", Prefix: "weekly", IntervalHours: 12, RetentionDays: 14,
	}); err != nil {
		t.Fatalf("save without moving the destination: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Prefix != "weekly" || loaded.AccessKeyID != "saved-access" || loaded.SecretKey != "saved-secret" {
		t.Fatalf("unchanged destination lost its keys or prefix: prefix=%q access=%q secret=%q",
			loaded.Prefix, loaded.AccessKeyID, loaded.SecretKey)
	}
}

func TestDisabledBackupStillRefusesToMoveSavedKeys(t *testing.T) {
	// Saved keys are used again as soon as the backup is enabled, so switching
	// it off does not make pointing them somewhere else safe.
	store := savedS3Store(t)
	err := store.Save(context.Background(), Config{
		Type: StorageTypeS3, Enabled: false, Endpoint: "https://attacker.example.test", Bucket: "arc-backups",
		Region: "us-east-1", Prefix: "arc", IntervalHours: 24, RetentionDays: 7,
	})
	if !errors.Is(err, ErrCredentialsNeededForMove) {
		t.Fatalf("moving a disabled backup with blank keys = %v, want ErrCredentialsNeededForMove", err)
	}
}

func TestMovingDestinationWithoutSavedKeysIsAllowed(t *testing.T) {
	store := newDestinationStore(t)
	ctx := context.Background()
	for _, endpoint := range []string{"https://first.example.test", "https://second.example.test"} {
		if err := store.Save(ctx, Config{
			Type: StorageTypeS3, Endpoint: endpoint, Bucket: "arc-backups", Region: "us-east-1",
			Prefix: "arc", IntervalHours: 24, RetentionDays: 7,
		}); err != nil {
			t.Fatalf("save %s before any keys are stored: %v", endpoint, err)
		}
	}
}

func TestMovingWebDAVURLWithoutThePasswordIsRefused(t *testing.T) {
	store := savedWebDAVStore(t)
	ctx := context.Background()
	err := store.Save(ctx, Config{
		Type: StorageTypeWebDAV, Enabled: true, WebDAVURL: "https://attacker.example.test/backups",
		WebDAVUsername: "arc", IntervalHours: 24, RetentionDays: 7,
	})
	if !errors.Is(err, ErrCredentialsNeededForMove) {
		t.Fatalf("moving the WebDAV URL with a blank password = %v, want ErrCredentialsNeededForMove", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebDAVURL != "https://dav.example.test/backups" || loaded.WebDAVPassword != "saved-password" {
		t.Fatalf("refused WebDAV move changed the destination or password: url=%q password=%q",
			loaded.WebDAVURL, loaded.WebDAVPassword)
	}
}

func TestMovingWebDAVURLWithThePasswordSavesIt(t *testing.T) {
	store := savedWebDAVStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, Config{
		Type: StorageTypeWebDAV, Enabled: true, WebDAVURL: "https://new-dav.example.test/backups",
		WebDAVUsername: "arc", WebDAVPassword: "new-password", IntervalHours: 24, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("move the WebDAV storage with a new password: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebDAVURL != "https://new-dav.example.test/backups" || loaded.WebDAVPassword != "new-password" {
		t.Fatalf("moved WebDAV storage = url %q password %q", loaded.WebDAVURL, loaded.WebDAVPassword)
	}
}

func TestSavingTheSameWebDAVURLKeepsThePassword(t *testing.T) {
	store := savedWebDAVStore(t)
	ctx := context.Background()
	// A trailing slash is the same destination once Save trims it.
	if err := store.Save(ctx, Config{
		Type: StorageTypeWebDAV, Enabled: true, WebDAVURL: "https://dav.example.test/backups/",
		WebDAVUsername: "arc", IntervalHours: 24, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("save without moving the WebDAV URL: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebDAVPassword != "saved-password" {
		t.Fatalf("unchanged WebDAV URL lost its password: %q", loaded.WebDAVPassword)
	}
}

func TestSavingWebDAVCannotMoveTheSavedS3Keys(t *testing.T) {
	store := savedS3Store(t)
	ctx := context.Background()
	// Switching storage type with the S3 endpoint untouched is an ordinary save.
	if err := store.Save(ctx, Config{
		Type: StorageTypeWebDAV, Enabled: true, Endpoint: "https://s3.example.test", Bucket: "arc-backups",
		Region: "us-east-1", WebDAVURL: "https://dav.example.test/backups", WebDAVUsername: "arc",
		WebDAVPassword: "webdav-password", Prefix: "arc", IntervalHours: 24, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("switch to WebDAV without moving the S3 destination: %v", err)
	}
	// The same form with the S3 endpoint edited would carry the saved keys to
	// a host they were never saved for.
	err := store.Save(ctx, Config{
		Type: StorageTypeWebDAV, Enabled: true, Endpoint: "https://attacker.example.test", Bucket: "arc-backups",
		Region: "us-east-1", WebDAVURL: "https://dav.example.test/backups", WebDAVUsername: "arc",
		WebDAVPassword: "webdav-password", Prefix: "arc", IntervalHours: 24, RetentionDays: 7,
	})
	if !errors.Is(err, ErrCredentialsNeededForMove) {
		t.Fatalf("editing the S3 endpoint from a WebDAV save = %v, want ErrCredentialsNeededForMove", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Endpoint != "https://s3.example.test" || loaded.AccessKeyID != "saved-access" || loaded.SecretKey != "saved-secret" {
		t.Fatalf("refused cross-type save changed the S3 destination or keys: endpoint=%q access=%q secret=%q",
			loaded.Endpoint, loaded.AccessKeyID, loaded.SecretKey)
	}
}

func TestSavingS3CannotMoveTheSavedWebDAVPassword(t *testing.T) {
	store := savedWebDAVStore(t)
	ctx := context.Background()
	err := store.Save(ctx, Config{
		Type: StorageTypeS3, Enabled: true, Endpoint: "https://s3.example.test", Bucket: "arc-backups",
		Region: "us-east-1", Prefix: "arc", AccessKeyID: "s3-access", SecretKey: "s3-secret",
		WebDAVURL: "https://attacker.example.test/backups", WebDAVUsername: "arc",
		IntervalHours: 24, RetentionDays: 7,
	})
	if !errors.Is(err, ErrCredentialsNeededForMove) {
		t.Fatalf("editing the WebDAV URL from an S3 save = %v, want ErrCredentialsNeededForMove", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebDAVURL != "https://dav.example.test/backups" || loaded.WebDAVPassword != "saved-password" {
		t.Fatalf("refused cross-type save changed the WebDAV destination or password: url=%q password=%q",
			loaded.WebDAVURL, loaded.WebDAVPassword)
	}
}
