package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The sign-up reviewer names a model like the trial does. A save that names a
// model which does not exist is refused and the setting is left as it was; the
// import already clears such a key, and the form used to accept it silently.
func TestSettingsSaveRefusesAMissingSignupReviewModel(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "settings-review-model.db"),
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
	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	svc := settings.New(db)
	providers := provider.NewStore(db, box)
	h := &Handlers{
		db: db, settings: svc, groups: group.NewStore(db), users: user.NewStore(db),
		models: model.NewStore(db, providers),
	}

	body := strings.NewReader(`{"` + settings.SignupReviewModel + `":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`)
	request := httptest.NewRequest(http.MethodPut, "/api/admin/settings", body)
	request = request.WithContext(auth.WithUser(request.Context(), founder))
	err = h.updateSettings(httptest.NewRecorder(), request)

	var refused *httpx.Error
	if !errors.As(err, &refused) || refused.Status < 400 || refused.Status >= 500 {
		t.Fatalf("save naming a missing sign-up reviewer model = %v, want a 4xx", err)
	}
	if got := svc.Get(settings.SignupReviewModel); got != "" {
		t.Errorf("sign-up reviewer model = %q after a refused save, want it unchanged", got)
	}
}
