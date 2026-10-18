package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/audit"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The trail is written after the handler, on a context the browser cannot
// cancel. Without a deadline of its own, a database that never hands out a
// connection kept the administrator's request open for as long as it did.
func TestAuditWriteDoesNotHoldTheRequestForever(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "audit-timeout.db"),
		MaxOpenConns: 1, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	previous := auditRecordTimeout
	auditRecordTimeout = 50 * time.Millisecond
	t.Cleanup(func() { auditRecordTimeout = previous })

	// The pool's only connection, held for the length of the test: the
	// trail's write waits for it the way it would for a stuck database.
	held, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Rollback() })

	h := &Handlers{db: db, Audit: audit.NewStore(db)}
	handler := h.audited(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/anything", nil)
	request = request.WithContext(auth.WithUser(request.Context(), user.User{
		ID: "01J00000000000000000000000", Username: "admin", Role: user.RoleSuperAdmin,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the request was still waiting on the trail's write")
	}
}
