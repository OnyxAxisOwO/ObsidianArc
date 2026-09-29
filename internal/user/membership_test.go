package user

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

func TestMembershipExpiry(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			cfg := config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "membership.db"), MaxOpenConns: 4}
			if driver == "postgres" {
				cfg = dbtest.Postgres(t, "membership expiry and renewal need both database engines")
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
			groups := group.NewStore(db)
			fallback, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
			if err != nil {
				t.Fatal(err)
			}
			premium, err := groups.Create(ctx, nil, group.CreateInput{Name: "Premium"})
			if err != nil {
				t.Fatal(err)
			}
			users := NewStore(db)
			account, err := users.Create(ctx, nil, CreateInput{Username: "member", PasswordHash: "hash", GroupID: premium.ID})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(time.Hour)
			set := func(groupID string, expiry int64) {
				t.Helper()
				if _, err := db.Exec(ctx, `UPDATE users SET group_id = ?, group_expires_at = ? WHERE id = ?`, groupID, expiry, account.ID); err != nil {
					t.Fatal(err)
				}
			}
			check := func(groupID string, expiry int64) {
				t.Helper()
				got, err := users.ByID(ctx, nil, account.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.GroupID != groupID || got.GroupExpiresAt != expiry {
					t.Fatalf("membership = %s/%d; want %s/%d", got.GroupID, got.GroupExpiresAt, groupID, expiry)
				}
			}

			set(premium.ID, at.UnixMilli())
			if err := users.ExpireMemberships(ctx, nil, at.Add(-time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			check(premium.ID, at.UnixMilli())
			if err := users.ExpireMemberships(ctx, nil, at); err != nil {
				t.Fatal(err)
			}
			check(fallback.ID, 0)
			set(premium.ID, 0)
			if err := users.ExpireMemberships(ctx, nil, at.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			check(premium.ID, 0)

			// A changed default is resolved when the term ends, even after a
			// restart. No process-local timer or remembered fallback is involved.
			newDefault, err := groups.Create(ctx, nil, group.CreateInput{Name: "New default", IsDefault: true})
			if err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-time.Hour).UnixMilli()
			set(premium.ID, past)
			users = NewStore(db)
			check(newDefault.ID, 0)
			set(premium.ID, past)
			rows, count, err := users.List(ctx, ListFilter{GroupID: newDefault.ID})
			if err != nil || count != 1 || len(rows) != 1 || rows[0].GroupExpiresAt != 0 {
				t.Fatalf("expired member list: %v, count %d, %v", rows, count, err)
			}
			set(premium.ID, past)
			loggedIn, hash, err := users.CredentialsByLogin(ctx, "member")
			if err != nil || hash != "hash" || loggedIn.GroupID != newDefault.ID {
				t.Fatalf("login expiry: %v, %v", loggedIn, err)
			}

			set(premium.ID, at.UnixMilli())
			if _, err := users.UpdateAdminFields(ctx, nil, account.ID, AdminUpdate{GroupID: &premium.ID}); err != nil {
				t.Fatal(err)
			}
			check(premium.ID, at.UnixMilli())
			if _, err := users.UpdateAdminFields(ctx, nil, account.ID, AdminUpdate{GroupID: &fallback.ID}); err != nil {
				t.Fatal(err)
			}
			check(fallback.ID, 0)
			set(premium.ID, at.UnixMilli())
			if err := users.MoveGroupMembers(ctx, nil, premium.ID, fallback.ID); err != nil {
				t.Fatal(err)
			}
			check(fallback.ID, 0)

			// Force the stale-read ordering: authentication has read an expired
			// membership, then another goroutine renews it before retirement.
			set(premium.ID, past)
			read := make(chan struct{})
			renewed := make(chan struct{})
			resolved := make(chan error, 1)
			go func() {
				stale, err := users.scan(db.QueryRow(ctx, `SELECT `+users.columns()+` FROM users WHERE id = ?`, account.ID))
				close(read)
				<-renewed
				if err == nil {
					var got User
					got, err = users.ResolveMembership(ctx, nil, stale)
					if err == nil && (got.GroupID != premium.ID || got.GroupExpiresAt != at.UnixMilli()) {
						err = ErrNotFound
					}
				}
				resolved <- err
			}()
			<-read
			until := at.UnixMilli()
			_, err = users.UpdateAdminFields(ctx, nil, account.ID, AdminUpdate{GroupID: &premium.ID, GroupExpiresAt: &until})
			close(renewed)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-resolved; err != nil {
				t.Fatalf("renewal lost to expired snapshot: %v", err)
			}
			check(premium.ID, until)
		})
	}
}
