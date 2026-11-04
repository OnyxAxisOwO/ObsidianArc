package user

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

// The rule about ban reasons is kept here rather than in the backoffice,
// because the plugin host changes status too, and a rule only the handler knew
// would not hold for it. Both engines: the reason is written by a statement
// that reads the row's status, and that has to parse the same on each.
func TestBanReasonStandsOnlyWhileBanned(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			cfg := config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "ban-reason.db"), MaxOpenConns: 4}
			if driver == "postgres" {
				cfg = dbtest.Postgres(t, "a reason is written by a statement that reads the status, which each engine parses for itself")
			}
			ctx := context.Background()
			db, err := database.Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			users := NewStore(db)

			sneaky, spam, second := "sneaky", "  spam  ", "second"
			banned, active := StatusDisabled, StatusActive

			bot, err := users.Create(ctx, nil, CreateInput{Username: "bot", PasswordHash: "hash", Status: banned, BanReason: "  bot  "})
			if err != nil {
				t.Fatal(err)
			}
			if bot.BanReason != "bot" {
				t.Errorf("created banned: reason %q, want the trimmed one", bot.BanReason)
			}

			member, err := users.Create(ctx, nil, CreateInput{Username: "member", PasswordHash: "hash", BanReason: sneaky})
			if err != nil {
				t.Fatal(err)
			}
			if member.BanReason != "" {
				t.Fatalf("created active: reason %q kept", member.BanReason)
			}

			write := func(in AdminUpdate) User {
				t.Helper()
				updated, err := users.UpdateAdminFields(ctx, nil, member.ID, in)
				if err != nil {
					t.Fatal(err)
				}
				return updated
			}
			expect := func(step string, got User, status Status, reason string) {
				t.Helper()
				if got.Status != status || got.BanReason != reason {
					t.Errorf("%s: status %q reason %q; want %q and %q", step, got.Status, got.BanReason, status, reason)
				}
			}

			expect("a reason alone on an active account", write(AdminUpdate{BanReason: &sneaky}), active, "")
			expect("a ban with its reason", write(AdminUpdate{Status: &banned, BanReason: &spam}), banned, "spam")
			expect("a reason alone on a banned account", write(AdminUpdate{BanReason: &second}), banned, "second")
			expect("a ban that restates no reason", write(AdminUpdate{Status: &banned}), banned, "second")
			// The plugin host's path: a status change with no reason at all.
			expect("activation by status alone", write(AdminUpdate{Status: &active}), active, "")
			expect("a fresh ban without a reason", write(AdminUpdate{Status: &banned}), banned, "")
			expect("activation with a reason sent", write(AdminUpdate{Status: &active, BanReason: &second}), active, "")
		})
	}
}
