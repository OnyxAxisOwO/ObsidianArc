package health

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A model the checker turns off on its own is exactly the case an operator
// cannot see happen — nobody is watching the availability screen at 3 a.m. —
// so it is the one health event that goes to the bell rather than only the
// log.
func TestDisablingAModelNotifiesAdministratorsWithAvailability(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "health-notify.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewStore(db, box)
	models := model.NewStore(db, providers)
	upstream, err := providers.Create(ctx, provider.CreateInput{
		Name: "Upstream", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test-0123", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	record, err := models.Create(ctx, model.CreateInput{
		ProviderID: upstream.ID, ModelID: "vendor/one", DisplayName: "Flagship", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	notifyStore := notify.NewStore(db)
	checker := &Checker{Store: NewStore(db), Models: models, Providers: providers, Notify: notifyStore}

	checker.apply(ctx, record, Status{State: StateDown, FailuresInARow: 3}, Policy{DisableAfter: 2})

	disabled := reload(t, models, record.ID)
	if disabled.Enabled || !disabled.AutoDisabled {
		t.Fatalf("model = %+v, want it turned off by the checker", disabled)
	}

	admin := user.User{Role: user.RoleAdmin, AdminPermissions: []string{"availability"}}
	notices, err := notifyStore.List(ctx, admin, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(notices) != 1 || notices[0].Kind != "model_disabled" ||
		notices[0].Params["model"] != "Flagship" || notices[0].Link != "/admin/availability" {
		t.Fatalf("notices = %+v, want one model_disabled notice naming the model", notices)
	}

	// An administrator without the availability grant does not see it.
	unrelated := user.User{Role: user.RoleAdmin, AdminPermissions: []string{"users"}}
	if notices, err := notifyStore.List(ctx, unrelated, 10, 0); err != nil || len(notices) != 0 {
		t.Fatalf("an unrelated admin's notices = %+v (err %v), want none", notices, err)
	}

	// Re-enabling — the checker's own reversal — is not itself announced a
	// second time; only the disable is newsworthy.
	checker.apply(ctx, disabled, Status{State: StateUp}, Policy{DisableAfter: 2})
	if notices, err := notifyStore.List(ctx, admin, 10, 0); err != nil || len(notices) != 1 {
		t.Fatalf("notices after re-enabling = %+v (err %v), want still just the one", notices, err)
	}
}

// Two instances sharing a database can both read the same enabled model in
// the same sweep and both decide, from that one stale snapshot, to disable
// it. The second apply below reuses the same pre-disable record on purpose —
// standing in for the second instance's copy of the read — and must find
// nothing left to flip, so it must not push the notice a second time.
func TestASecondApplyOnAnAlreadyDisabledModelDoesNotPushAgain(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "health-notify-race.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewStore(db, box)
	models := model.NewStore(db, providers)
	upstream, err := providers.Create(ctx, provider.CreateInput{
		Name: "Upstream", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test-0123", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	record, err := models.Create(ctx, model.CreateInput{
		ProviderID: upstream.ID, ModelID: "vendor/one", DisplayName: "Flagship", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	notifyStore := notify.NewStore(db)
	checker := &Checker{Store: NewStore(db), Models: models, Providers: providers, Notify: notifyStore}
	policy := Policy{DisableAfter: 2}
	status := Status{State: StateDown, FailuresInARow: 3}

	// The first instance's pass: this one actually flips the row and pushes.
	checker.apply(ctx, record, status, policy)
	disabled := reload(t, models, record.ID)
	if disabled.Enabled || !disabled.AutoDisabled {
		t.Fatalf("model = %+v, want it turned off by the first apply", disabled)
	}
	admin := user.User{Role: user.RoleAdmin, AdminPermissions: []string{"availability"}}
	if notices, err := notifyStore.List(ctx, admin, 10, 0); err != nil || len(notices) != 1 {
		t.Fatalf("notices after the first apply = %+v (err %v), want one", notices, err)
	}

	// The second instance's pass, from the same stale snapshot (`record`,
	// still enabled in memory) as the first: the guard inside
	// DisableIfEnabled must find the row already disabled and change nothing.
	checker.apply(ctx, record, status, policy)
	if notices, err := notifyStore.List(ctx, admin, 10, 0); err != nil || len(notices) != 1 {
		t.Fatalf("notices after the second apply = %+v (err %v), want still just the one", notices, err)
	}
}
