package database

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
)

// Installing a plugin on a running server runs MigrateIn inside the install's
// transaction. Two instances installing the same plugin at once are the race
// the claim in Migrate closes: the loser must skip the version the winner
// recorded rather than run its CREATE TABLE again. SQLite serialises the two
// transactions behind its write lock, so only the PostgreSQL run can show the
// failure this guards against.
func TestConcurrentMigrateInAppliesEachVersionOnce(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		for round := 0; round < 6; round++ {
			assertMigrateInOnceBy(t, config.Database{
				Driver:       "sqlite",
				DSN:          filepath.Join(t.TempDir(), "install.db"),
				MaxOpenConns: 2,
				MaxIdleConns: 1,
			})
		}
	})
	t.Run("postgres", func(t *testing.T) {
		for round := 0; round < 3; round++ {
			assertMigrateInOnceBy(t, dbtest.Postgres(t, "the concurrent plugin-install race on PostgreSQL goes unchecked"))
		}
	})
}

func assertMigrateInOnceBy(t *testing.T, cfg config.Database) {
	t.Helper()
	ctx := context.Background()
	plugin := fstest.MapFS{"plugq_0001_a.sql": {Data: []byte(`CREATE TABLE plugq_a (id TEXT PRIMARY KEY);`)}}

	// MigrateIn assumes the bookkeeping table exists; creating it here keeps the
	// race on the plugin's migration rather than on a concurrent CREATE TABLE IF
	// NOT EXISTS, which can collide on PostgreSQL's catalog.
	setup, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open for setup: %v", err)
	}
	if _, err := setup.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at BIGINT NOT NULL
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	_ = setup.Close()

	var handles [2]*DB
	for i := range handles {
		db, err := Open(ctx, cfg)
		if err != nil {
			t.Fatalf("open handle %d: %v", i, err)
		}
		defer db.Close()
		handles[i] = db
	}

	var (
		applied [2][]string
		errs    [2]error
		wg      sync.WaitGroup
	)
	for i := range handles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = handles[i].Tx(ctx, func(tx *Tx) error {
				var err error
				applied[i], err = handles[i].MigrateIn(ctx, tx, plugin)
				return err
			})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("install %d refused: %v", i, err)
		}
	}
	if n := len(applied[0]) + len(applied[1]); n != 1 {
		t.Fatalf("the plugin's migration ran %d times (applied %v and %v); want once", n, applied[0], applied[1])
	}
	recorded, err := AppliedVersions(ctx, handles[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"plugq_0001_a"}; !slices.Equal(recorded, want) {
		t.Errorf("recorded versions = %v, want %v", recorded, want)
	}
}
