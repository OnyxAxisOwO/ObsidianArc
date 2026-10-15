package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A policy that requires a second step may be saved only by an administrator
// who has one. The saver's account is read when the write commits: a request
// that began while their second step was still on must not be let through
// after another request has switched it off.
func TestSettingsSaveChecksTheSaversTwoStepAtCommit(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "settings-two-step.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	grp, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	founder, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "founder", Nickname: "Founder", PasswordHash: "hash", Role: user.RoleSuperAdmin, GroupID: grp.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := settings.New(db)
	h := &Handlers{db: db, settings: svc, groups: group.NewStore(db), users: user.NewStore(db)}

	save := func(actor user.User) error {
		body := strings.NewReader(`{"` + settings.TwoFactorPolicy + `":"` + settings.TwoFactorEveryone + `"}`)
		request := httptest.NewRequest(http.MethodPut, "/api/admin/settings", body)
		request = request.WithContext(auth.WithUser(request.Context(), actor))
		return h.updateSettings(httptest.NewRecorder(), request)
	}

	// The request saw a second step on. The account has switched it off since,
	// and the save must not write a policy this administrator cannot satisfy.
	seen := founder
	seen.TwoFactorAt = time.Now().UnixMilli()
	err = save(seen)
	var refused *httpx.Error
	if !errors.As(err, &refused) || refused.Code != "two_factor_self" {
		t.Fatalf("save with a second step switched off since the request began = %v, want two_factor_self", err)
	}
	if got := svc.Get(settings.TwoFactorPolicy); got != settings.TwoFactorOptional {
		t.Fatalf("policy = %q after a refused save, want it unchanged", got)
	}

	// An import drops the same key rather than refusing the file, and must not
	// let it through on the snapshot either.
	file := `{"` + settings.TwoFactorPolicy + `":"` + settings.TwoFactorEveryone + `"}`
	imported := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/settings/import", strings.NewReader(file))
	request = request.WithContext(auth.WithUser(request.Context(), seen))
	if err := h.importSettings(imported, request); err != nil {
		t.Fatalf("import with a second step switched off since the request began: %v", err)
	}
	var result struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(imported.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != settings.TwoFactorPolicy {
		t.Fatalf("import skipped %v, want only %s", result.Skipped, settings.TwoFactorPolicy)
	}
	if got := svc.Get(settings.TwoFactorPolicy); got != settings.TwoFactorOptional {
		t.Fatalf("policy = %q after an import that dropped it, want it unchanged", got)
	}

	// The ordinary case still saves: the second step is on in the row too.
	if _, err := db.Exec(ctx, `UPDATE users SET two_factor_at = ? WHERE id = ?`, seen.TwoFactorAt, founder.ID); err != nil {
		t.Fatal(err)
	}
	if err := save(seen); err != nil {
		t.Fatalf("save with the second step on: %v", err)
	}
	if got := svc.Get(settings.TwoFactorPolicy); got != settings.TwoFactorEveryone {
		t.Fatalf("policy = %q after an accepted save, want %q", got, settings.TwoFactorEveryone)
	}
}
