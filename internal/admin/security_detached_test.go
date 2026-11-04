package admin

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The password reset has committed by the time its audit row is written, so a
// client that hung up in between must not cost the row: the write runs on a
// context that outlives the request.
func TestAccountActionIsRecordedAfterTheClientHangsUp(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "admin-security.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := securityevents.NewStore(db)
	h := &Handlers{security: store}

	actor := user.User{ID: "actor-account", Username: "founder"}
	target := user.User{ID: "target-account", Username: "reset-target"}

	request := httptest.NewRequest("POST", "/api/admin/users/target-account/password", nil)
	gone, hangUp := context.WithCancel(request.Context())
	hangUp()
	h.recordAccountAction(request.WithContext(gone), securityevents.EventPasswordReset, actor, target, "password reset by an administrator")

	events, total, err := store.List(ctx, securityevents.Filter{UserID: target.ID})
	if err != nil {
		t.Fatalf("list security events: %v", err)
	}
	if total != 1 || len(events) != 1 || events[0].Event != securityevents.EventPasswordReset {
		t.Fatalf("security log = %+v (total %d), want one %s row for the target", events, total, securityevents.EventPasswordReset)
	}
}
