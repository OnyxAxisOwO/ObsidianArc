package server

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Two instances of this server pointed at one database can start at the same
// moment — a rolling restart, a replica coming up, an operator running the
// binary twice. Both then run Bootstrap against an empty table.
//
// The re-check inside the transaction used to stand alone, with a comment
// claiming it covered exactly this. It did not: a count reads what is
// committed and blocks nobody.
//
// Which engine this runs on decides what it can prove, and the two subtests
// say so rather than reading as one result:
//
//   - SQLite serialises writers at the database, so the race cannot happen
//     there and this subtest cannot fail. It is here to show the lock did not
//     break the ordinary path — nothing more. Do not read a green from it as
//     evidence the lock works.
//   - PostgreSQL is where the race is real. Removing settings.Lock and running
//     this is what fails. CI supplies the DSN; without one it skips, and skips
//     loudly rather than passing.
func TestConcurrentBootstrapsCreateOneAdministrator(t *testing.T) {
	t.Run("sqlite proves only that nothing broke", func(t *testing.T) {
		dir := t.TempDir()
		raceBootstrap(t, dir, config.Database{
			Driver: "sqlite", DSN: filepath.Join(dir, "bootstrap.db"),
			MaxOpenConns: 4, MaxIdleConns: 2,
		})
	})

	t.Run("postgres is where the race is", func(t *testing.T) {
		dsn := os.Getenv("OBSIDIAN_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set OBSIDIAN_TEST_POSTGRES_DSN — the SQLite run above cannot fail this")
		}
		raceBootstrap(t, t.TempDir(), config.Database{
			Driver: "postgres", DSN: dsn, MaxOpenConns: 8, MaxIdleConns: 4,
		})
	})
}

func raceBootstrap(t *testing.T, dir string, dbCfg config.Database) {
	t.Helper()
	ctx := context.Background()

	cfg := config.Config{
		DataDir:   dir,
		Database:  dbCfg,
		Session:   config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
		Password:  config.Password{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32, MaxParallel: 4},
		SecretKey: []byte("a-test-instance-secret-value-here"),
		Bootstrap: config.Bootstrap{Username: "founder", Password: "a-good-password"},
	}

	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A shared Postgres carries whatever the last run left behind, and this
	// test is about an empty instance.
	if dbCfg.Driver == "postgres" {
		for _, table := range []string{"users", "user_groups", "settings"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
		}
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS schema_migrations CASCADE")
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	groups := group.NewStore(db)
	users := user.NewStore(db)
	authService := auth.NewService(db, users, groups, set, mail.New(mail.Config{}), cfg)

	// The default group first, so the racing half is only the administrator.
	seed := cfg
	seed.Bootstrap = config.Bootstrap{}
	if err := Bootstrap(ctx, db, groups, users, authService, seed); err != nil {
		t.Fatalf("seed the default group: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errs <- Bootstrap(ctx, db, groups, users, authService, cfg)
		}()
	}
	close(start)
	workers.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent bootstrap: %v", err)
		}
	}

	total, err := users.Count(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("four simultaneous bootstraps created %d accounts, want 1", total)
	}
	admins, err := users.CountActiveAdmins(ctx, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Fatalf("created %d administrators, want 1", admins)
	}
}
