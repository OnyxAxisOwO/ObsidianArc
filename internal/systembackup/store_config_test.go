package systembackup

import (
	"context"
	"errors"
	"testing"
)

// A storage type the backend factory does not know is refused when it is
// saved, not when someone later turns the backup on and the run fails.
func TestSaveRefusesAStorageTypeItDoesNotKnow(t *testing.T) {
	store := newDestinationStore(t)
	err := store.Save(context.Background(), Config{
		Type: "ftp", Enabled: false, IntervalHours: 24, RetentionDays: 7,
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("saving an unknown storage type while disabled = %v, want a ValidationError", err)
	}
}

// A password in the WebDAV URL would be stored in plain text and echoed to the
// admin page, so the URL is refused whether or not the backup is enabled.
func TestSaveRefusesAWebDAVURLThatCarriesACredential(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		store := newDestinationStore(t)
		ctx := context.Background()
		err := store.Save(ctx, Config{
			Type: StorageTypeWebDAV, Enabled: enabled,
			WebDAVURL: "https://arc:hunter2@dav.example.test/backups", WebDAVUsername: "arc",
			WebDAVPassword: "typed-password", IntervalHours: 24, RetentionDays: 7,
		})
		var invalid *ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("enabled=%v: saving a WebDAV URL with userinfo = %v, want a ValidationError", enabled, err)
		}
		loaded, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.WebDAVURL != "" || loaded.WebDAVPassword != "" {
			t.Fatalf("enabled=%v: refused URL was stored: url %q", enabled, loaded.WebDAVURL)
		}
	}
}
