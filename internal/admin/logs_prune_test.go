package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A prune is recorded twice: once as a request before anything is deleted, and
// once with the number removed after it succeeds. The first entry used to say
// the rows were already removed, so a prune that failed left a security log
// claiming a deletion that never happened.
func TestAPruneRecordsTheRequestAndThenTheOutcome(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "logs-prune.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := &Handlers{db: db, requests: reqlog.NewStore(db), security: securityevents.NewStore(db)}
	actor := user.User{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Username: "founder", Role: user.RoleSuperAdmin}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/logs/prune", strings.NewReader(`{"days":30}`))
	request = request.WithContext(auth.WithUser(request.Context(), actor))
	if err := h.pruneLogs(httptest.NewRecorder(), request); err != nil {
		t.Fatalf("pruneLogs: %v", err)
	}

	events, _, err := h.security.List(ctx, securityevents.Filter{Event: eventLogPrune})
	if err != nil {
		t.Fatal(err)
	}
	decisions := map[string]string{}
	for _, event := range events {
		decisions[event.Decision] = event.Reason
	}
	requested, ok := decisions["prune"]
	if !ok {
		t.Fatalf("no entry recording the prune request; entries = %v", decisions)
	}
	if strings.Contains(requested, "removed") {
		t.Errorf("the request entry %q claims the rows were removed before the delete ran", requested)
	}
	outcome, ok := decisions["pruned"]
	if !ok {
		t.Fatalf("no entry recording the completed prune; entries = %v", decisions)
	}
	if !strings.Contains(outcome, "removed: 0") {
		t.Errorf("the outcome entry %q does not carry the removed count", outcome)
	}
}
