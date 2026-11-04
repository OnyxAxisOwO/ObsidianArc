package user

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// openHashStore returns the user store over a fresh database of the given
// engine, holding one account, "member", whose stored hash is old-hash.
func openHashStore(t *testing.T, driver string) (*Store, string) {
	t.Helper()
	cfg := config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "replace.db"), MaxOpenConns: 4}
	if driver == "postgres" {
		cfg = dbtest.Postgres(t, "a conditional replace has to hold on both database engines")
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
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	users := NewStore(db)
	account, err := users.Create(ctx, nil, CreateInput{Username: "member", PasswordHash: "old-hash"})
	if err != nil {
		t.Fatal(err)
	}
	return users, account.ID
}

// A rehash replaces the hash it verified and nothing else. The condition is the
// whole guarantee: a password changed after the check is already stored, and a
// replace that ignored what is stored would write the old hash back over it. The
// condition is checked on both engines, because each one evaluates it under its
// own locking.
func TestReplacePasswordHashOnlyReplacesTheVerifiedHash(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			users, account := openHashStore(t, driver)
			ctx := context.Background()

			replaced, err := users.ReplacePasswordHash(ctx, nil, account, "stale-hash", "new-hash")
			if err != nil {
				t.Fatal(err)
			}
			if replaced {
				t.Fatal("a hash that is no longer stored was replaced")
			}
			stored, err := users.PasswordHash(ctx, nil, account)
			if err != nil {
				t.Fatal(err)
			}
			if stored != "old-hash" {
				t.Fatalf("the stored hash after a refused replace = %q, want it untouched", stored)
			}

			replaced, err = users.ReplacePasswordHash(ctx, nil, account, "old-hash", "new-hash")
			if err != nil {
				t.Fatal(err)
			}
			if !replaced {
				t.Fatal("the hash that was verified was not replaced")
			}
			stored, err = users.PasswordHash(ctx, nil, account)
			if err != nil {
				t.Fatal(err)
			}
			if stored != "new-hash" {
				t.Fatalf("the stored hash after a replace = %q, want new-hash", stored)
			}
		})
	}
}

// A password change that lands while a sign-in is writing its upgraded hash has
// to survive that write. Each round starts from the hash the sign-in verified,
// then the replace and the change are released together, so the two statements
// race on one row. Whichever the database runs first, the change is what ends up
// stored: the replace only matches the hash it verified, so when it runs second
// it finds the change and does nothing. An unconditional write would put the
// upgraded hash back over the change on exactly those rounds.
//
// The launch order alternates so both orders occur even where the scheduler
// favours the goroutine started last. Each round asserts the outcome, and the
// test asserts both orders happened, because a run that only ever exercised one
// of them would prove nothing about the other.
func TestAPasswordChangeRacingARehashIsNeverOverwritten(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			users, account := openHashStore(t, driver)
			ctx := context.Background()

			const rounds = 100
			replaceFirst, changeFirst := 0, 0
			for i := range rounds {
				verified := fmt.Sprintf("verified-%03d", i)
				upgraded := fmt.Sprintf("upgraded-%03d", i)
				changed := fmt.Sprintf("changed-%03d", i)
				if err := users.SetPasswordHash(ctx, nil, account, verified); err != nil {
					t.Fatal(err)
				}

				var replaced bool
				var replaceErr, changeErr error
				replace := func() {
					replaced, replaceErr = users.ReplacePasswordHash(ctx, nil, account, verified, upgraded)
				}
				change := func() {
					changeErr = users.SetPasswordHash(ctx, nil, account, changed)
				}
				runs := []func(){replace, change}
				if i%2 == 1 {
					runs = []func(){change, replace}
				}

				gate := make(chan struct{})
				var workers sync.WaitGroup
				for _, run := range runs {
					workers.Add(1)
					go func() {
						defer workers.Done()
						<-gate
						run()
					}()
				}
				close(gate)
				workers.Wait()

				if replaceErr != nil || changeErr != nil {
					t.Fatalf("round %d: replace = %v, change = %v", i, replaceErr, changeErr)
				}
				stored, err := users.PasswordHash(ctx, nil, account)
				if err != nil {
					t.Fatal(err)
				}
				if stored != changed {
					t.Fatalf("round %d: the stored hash is %q, not the password change %q; the replace reported replaced=%v", i, stored, changed, replaced)
				}
				// A replace that matched the verified hash ran before the change.
				if replaced {
					replaceFirst++
				} else {
					changeFirst++
				}
			}
			if replaceFirst == 0 || changeFirst == 0 {
				t.Fatalf("only one order was exercised: the replace ran first in %d rounds and second in %d", replaceFirst, changeFirst)
			}
		})
	}
}
