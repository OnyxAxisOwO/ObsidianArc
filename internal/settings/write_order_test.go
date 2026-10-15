package settings

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func openMigratedSettings(t *testing.T) (*database.DB, *Service) {
	t.Helper()
	ctx := t.Context()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "settings_write_order.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := New(db)
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	return db, svc
}

// Two writers to one key must leave the cache on the value the database holds.
// Writes that commit in one order and update the cache in the other would leave
// it on the older value until restart, which is the bug this guards.
//
// This check is probabilistic. writeMu serialises the two writers, so without
// it the bad interleaving still needs the scheduler to hit the window between
// a commit and its cache update; a run can pass without the fix. The forced
// version is TestASaveWaitsForAnotherBetweenItsWriteAndItsCacheUpdate.
func TestConcurrentSetsLeaveTheCacheOnTheStoredValue(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)

	const writers, rounds = 8, 40
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(value string) {
				defer wg.Done()
				if err := svc.Set(ctx, "security.two_factor_policy", value); err != nil {
					t.Errorf("set: %v", err)
				}
			}(fmt.Sprintf("v%d-%d", round, w))
		}
		wg.Wait()

		var stored string
		if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`,
			"security.two_factor_policy").Scan(&stored); err != nil {
			t.Fatalf("read stored value: %v", err)
		}
		if got := svc.Get("security.two_factor_policy"); got != stored {
			t.Fatalf("round %d: cache holds %q, database holds %q", round, got, stored)
		}
	}
}

// The interleaving TestConcurrentSetsLeaveTheCacheOnTheStoredValue can only
// hope to hit, forced. The first save is paused after its database write and
// before its cache update, and a second save to the same key starts. The second
// must wait for the first to finish; if it finishes inside the window, the
// first then writes its older value over the cache, and the cache and the
// database disagree.
func TestASaveWaitsForAnotherBetweenItsWriteAndItsCacheUpdate(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)
	const key = "security.two_factor_policy"

	// A flag rather than sync.Once: Once makes the second save wait inside the
	// hook for the first, which would pass the test without writeMu.
	inWindow := make(chan struct{})
	release := make(chan struct{})
	var paused atomic.Bool
	svc.afterWrite = func(string) {
		if paused.CompareAndSwap(false, true) {
			close(inWindow)
			<-release
		}
	}

	first := make(chan error, 1)
	go func() { first <- svc.Set(ctx, key, "first") }()
	select {
	case <-inWindow:
	case <-time.After(5 * time.Second):
		t.Fatal("the first save never reached its cache update")
	}

	second := make(chan error, 1)
	go func() { second <- svc.Set(ctx, key, "second") }()
	select {
	case err := <-second:
		close(release)
		<-first
		t.Fatalf("the second save finished inside the first's window (err %v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second save: %v", err)
	}

	var stored string
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&stored); err != nil {
		t.Fatalf("read stored value: %v", err)
	}
	if got := svc.Get(key); got != "second" || stored != "second" {
		t.Fatalf("cache holds %q, database holds %q; both should be \"second\"", got, stored)
	}
}

// A plugin install commits its values outside writeMu and then calls Remember.
// If an administrator's save committed after the install and updated the cache
// first, Remember must not write the install's older value back over it.
func TestRememberTakesTheStoredRowNotTheValueItWasGiven(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)
	const key = SiteName

	installed := map[string]string{key: "installed"}
	if err := db.Tx(ctx, func(tx *database.Tx) error {
		return svc.WriteMany(ctx, tx, installed)
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := svc.Set(ctx, key, "saved"); err != nil {
		t.Fatalf("save: %v", err)
	}
	svc.Remember(installed)
	if got := svc.Get(key); got != "saved" {
		t.Fatalf("cache holds %q after Remember; the database holds \"saved\"", got)
	}
}

// An uninstall deletes a plugin's rows and then calls Forget. A save that wrote
// the same key again after the delete has already put its value in the cache,
// and Forget must not drop it.
func TestForgetKeepsARowWrittenAfterTheDeletion(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)
	const key = SiteName

	if err := svc.Set(ctx, key, "before"); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := db.Tx(ctx, func(tx *database.Tx) error {
		return svc.DeleteMany(ctx, tx, []string{key})
	}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if err := svc.Set(ctx, key, "after"); err != nil {
		t.Fatalf("save after the delete: %v", err)
	}
	svc.Forget([]string{key})
	if got := svc.Get(key); got != "after" {
		t.Fatalf("cache holds %q after Forget; the database holds \"after\"", got)
	}
}

// A Set issued inside a transaction that holds the instance lock cannot succeed
// while that transaction is open, and Lock says it must not be attempted. The
// test pins the failure mode: the call returns an error instead of waiting
// forever. SQLite waits out its busy timeout (5s) and does not honour the
// context while it waits; Postgres cancels on the context. The bound below is
// past the first, so the test holds on both. It does not cover a Set that
// queues behind a checked save's writeMu, which is the deadlock Lock warns about.
func TestASetInsideTheInstanceLockReturnsAnError(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)

	var setErr error
	done := make(chan error, 1)
	go func() {
		done <- db.Tx(ctx, func(tx *database.Tx) error {
			if err := Lock(ctx, tx); err != nil {
				return err
			}
			callCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer cancel()
			setErr = svc.Set(callCtx, SiteName, "inside the lock")
			return nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("transaction: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Set inside the instance lock did not return")
	}
	if setErr == nil {
		t.Fatal("Set succeeded inside the instance lock")
	}
}

// A failed read at boot used to leave the login backgrounds and the logo empty
// for the life of the process. Refusing to start is the only place to say so.
func TestLoadRefusesWhenTheBackgroundTableCannotBeRead(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)

	if _, err := db.Exec(ctx, `DROP TABLE login_backgrounds`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := svc.Load(ctx); err == nil {
		t.Fatal("Load succeeded without the login background table")
	}
}

// The login background's cache keeps the UpdatedAt that becomes the ?v= cache
// buster, and whether it is a page. Two writers to one variant must leave the
// cache describing the row that is stored, or the page advertises a picture
// that is no longer there until restart.
//
// Probabilistic, for the reason given on TestConcurrentSetsLeaveTheCacheOnTheStoredValue.
func TestConcurrentBackgroundWritesLeaveTheCacheOnTheStoredRow(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)

	const writers, rounds = 8, 20
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				var err error
				if w%2 == 0 {
					_, err = svc.SetHTMLBackground(ctx, LoginBgLandscapeLight, fmt.Sprintf("<p>%d-%d</p>", round, w))
				} else {
					_, err = svc.SetLoginBackground(ctx, LoginBgLandscapeLight, "image/png",
						[]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"))
				}
				if err != nil {
					t.Errorf("store background: %v", err)
				}
			}(w)
		}
		wg.Wait()

		var mime string
		var at int64
		if err := db.QueryRow(ctx, `SELECT mime, updated_at FROM login_backgrounds WHERE variant = ?`,
			LoginBgLandscapeLight).Scan(&mime, &at); err != nil {
			t.Fatalf("read stored background: %v", err)
		}
		cached := svc.LoginBackgrounds()[LoginBgLandscapeLight]
		if cached.UpdatedAt != at || cached.HTML != (mime == HTMLBackgroundMime) {
			t.Fatalf("round %d: cache says %+v, database says mime %q at %d", round, cached, mime, at)
		}
	}
}

// A plugin install commits outside writeMu, so it can commit inside an
// administrator's save window: after the save's write and before its cache
// update. Its Remember must wait for the save and then re-read the row, so the
// cache ends on what the database holds whichever of the two lands last.
func TestAPluginInstallCommittingInsideASaveWindowLeavesTheStoredValue(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)
	const key = SiteName

	inWindow := make(chan struct{})
	release := make(chan struct{})
	var paused atomic.Bool
	svc.afterWrite = func(string) {
		if paused.CompareAndSwap(false, true) {
			close(inWindow)
			<-release
		}
	}
	saved := make(chan error, 1)
	go func() { saved <- svc.Set(ctx, key, "saved") }()
	select {
	case <-inWindow:
	case <-time.After(5 * time.Second):
		t.Fatal("the save never reached its cache update")
	}

	installed := map[string]string{key: "installed"}
	if err := db.Tx(ctx, func(tx *database.Tx) error {
		return svc.WriteMany(ctx, tx, installed)
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	remembered := make(chan struct{})
	go func() {
		svc.Remember(installed)
		close(remembered)
	}()
	select {
	case <-remembered:
		close(release)
		<-saved
		t.Fatal("Remember updated the cache inside the save's window")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-saved; err != nil {
		t.Fatalf("save: %v", err)
	}
	<-remembered

	var stored string
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&stored); err != nil {
		t.Fatalf("read stored value: %v", err)
	}
	if got := svc.Get(key); got != stored || stored != "installed" {
		t.Fatalf("cache holds %q, database holds %q; both should be \"installed\"", got, stored)
	}
}
