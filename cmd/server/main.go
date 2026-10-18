// Command server is the whole of Obsidian Arc: one process serving the API
// and the frontend, talking to one database.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/systembackup"
)

// The build's identity, stamped in by the Makefile as
// `-ldflags "-X main.version=v1.2.3"` — the git tag the build was cut from,
// or `git describe`'s account of how far past one it is (v1.2.3-14-g1a2b3c4,
// with -dirty on the end for uncommitted changes). So "which build is this
// server running" is answerable from /api/health, and the answer can be found
// again in git.
//
// A plain `go build` leaves it as "dev", which is the honest answer for one.
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "restore-backup" {
		if err := restoreBackup(os.Args[2:]); err != nil {
			slog.Error("backup restore failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func restoreBackup(args []string) error {
	flags := flag.NewFlagSet("restore-backup", flag.ContinueOnError)
	archivePath := flags.String("file", "", "path to the .arcbackup archive")
	keyLost := flags.Bool("key-lost", false, "restore although the original OBSIDIAN_SECRET_KEY is gone; sealed values (provider keys, 2FA secrets) become unreadable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *archivePath == "" || flags.NArg() != 0 {
		return errors.New("usage: obsidian-arc restore-backup --file <archive.arcbackup>")
	}
	if !*keyLost {
		if err := requireExistingRestoreKey(); err != nil {
			return err
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// nil skips the archive's key check; see verifyKeyCheck.
	archiveKey := cfg.SecretKey
	if *keyLost {
		archiveKey = nil
	}
	if err := systembackup.VerifyArchiveKey(*archivePath, archiveKey); err != nil {
		return err
	}
	setupLogging(cfg)
	ctx := context.Background()
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	// The schema the archive was taken from, not this binary's: the packages
	// it carries bring their own migrations, and what was compiled in or is
	// bundled with this deployment is offered for the versions it names.
	// plugin_installs, restored with the rest, then says which plugins the
	// instance has.
	extra := append(plugin.Migrations(), plugin.BundledMigrations(cfg.PluginDir)...)
	if err := systembackup.PrepareRestore(ctx, db, *archivePath, archiveKey, extra...); err != nil {
		return err
	}
	return systembackup.RestoreArchive(ctx, db, *archivePath, archiveKey)
}

// config.Load creates and keeps a new secret.key when neither the environment
// nor the data directory holds one. A restore that reached it without the
// original key would leave a key behind that cannot open the archive, so this
// gate only looks at what is there and never creates anything.
func requireExistingRestoreKey() error {
	if strings.TrimSpace(os.Getenv("OBSIDIAN_SECRET_KEY")) != "" {
		return nil
	}
	dataDir := os.Getenv("OBSIDIAN_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	keyPath := filepath.Join(dataDir, config.SecretKeyFile)
	key, err := os.ReadFile(keyPath)
	if err != nil || len(strings.TrimSpace(string(key))) < config.MinSecretLen {
		return errors.New("restore requires the original OBSIDIAN_SECRET_KEY or the original secret.key in the configured data directory")
	}
	return nil
}

func run() error {
	started := time.Now()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogging(cfg)

	slog.Info("starting obsidian arc",
		"version", version,
		"addr", cfg.Addr,
		"driver", cfg.Database.Driver,
		"data_dir", cfg.DataDir,
		"dev", cfg.Dev,
	)
	if cfg.GeneratedSecret() {
		slog.Warn("using a generated secret key from the data directory; set OBSIDIAN_SECRET_KEY to share one across instances",
			"file", filepath.Join(cfg.DataDir, config.SecretKeyFile))
	}

	// Connecting and migrating get their own short deadline: a database that
	// is not there should fail the boot quickly rather than hang a service
	// manager's start timeout.
	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBoot()

	db, err := database.Open(bootCtx, cfg.Database)
	if err != nil {
		return err
	}
	// Closed through closeDB rather than db.Close, so that the background loops
	// can close it after their final write; see stopBackgroundThenClose. This
	// defer covers the returns that happen before the loops start.
	closeDB := sync.OnceValue(db.Close)
	defer closeDB()

	// The core's alone. An installed plugin's run when the server loads the
	// plugin states, and an uninstalled one's run when somebody installs
	// it — its tables are not this instance's until then.
	applied, err := db.Migrate(bootCtx)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		slog.Info("applied migrations", "count", len(applied), "versions", applied)
	}

	app, err := server.New(bootCtx, server.Deps{
		Config:  cfg,
		DB:      db,
		Version: version,
		Started: started,
	})
	if err != nil {
		return err
	}

	// The loops are stopped and waited for before the database closes, on
	// every return path that gets this far: see stopBackgroundThenClose.
	backgroundCtx, stopBackground := context.WithCancel(context.Background())
	janitorDone := app.StartJanitor(backgroundCtx)
	stopJanitor := sync.OnceValue(func() bool {
		return stopBackgroundThenClose(stopBackground, janitorDone, closeDB)
	})
	defer stopJanitor()

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: app.Handler(),
		// No WriteTimeout: a streamed answer legitimately takes minutes, and
		// a global write deadline would sever it mid-sentence. httpx.SSE arms
		// a per-frame write deadline instead (writeDeadline there), so a
		// stalled reader is cut loose without capping how long an answer runs.
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       5 * time.Minute,
		IdleTimeout:       120 * time.Second,
		// The default is 1 MiB, far beyond every credential and header this
		// API accepts. A smaller ceiling bounds anonymous header-memory abuse.
		MaxHeaderBytes: 64 << 10,
		ErrorLog:       slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Two listeners now, and one error channel: an operator who configured
	// SSH and got a port already in use has a broken console, which is worth
	// the same exit the HTTP port would get rather than a warning nobody
	// reads. Buffered for both, so neither goroutine blocks on send after the
	// select below has already returned.
	serveErr := make(chan error, 2)
	if sshServer := app.SSH(); sshServer != nil {
		// The configured address rather than sshServer.Addr(), which is empty
		// until the socket is bound — and binding happens inside
		// ListenAndServe, below. The fingerprint is here so an operator can
		// compare it against what their client shows on first connection.
		slog.Info("console ssh listening",
			"addr", cfg.Console.SSHAddr, "host_key", sshServer.Fingerprint())
		go func() {
			if err := sshServer.ListenAndServe(); err != nil {
				serveErr <- fmt.Errorf("console ssh: %w", err)
				return
			}
			serveErr <- nil
		}()
	}
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "boot_ms", time.Since(started).Milliseconds())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case sig := <-shutdown:
		slog.Info("shutting down", "signal", sig.String())
	}

	// In-flight streams get a grace period to finish; the deadline is what
	// stops a stuck upstream from holding the process open forever.
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelStop()
	// Before the HTTP server, because a console session's commands run
	// through the admin handlers this process is still serving.
	if sshServer := app.SSH(); sshServer != nil {
		if err := sshServer.Shutdown(stopCtx); err != nil {
			slog.Error("console ssh did not stop cleanly", "error", err)
		}
	}
	if err := shutdownHTTP(stopCtx, srv); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	// Read before stopJanitor: its wait can run past the deadline, and that does
	// not mean an answer was cut.
	answersCut := stopCtx.Err() != nil
	reportStopped(stopJanitor(), answersCut)
	return nil
}

// reportStopped logs how the process ended. "stopped" promises that the final
// request-log write has landed, so it is logged only when the background loops
// returned in time and no answer was still running at the deadline: an answer
// that completes after the database closes loses its row. The scheduled backups
// are waited on with the loops, since a backup's final status is a database
// write too. A backup started from the admin page is not: it runs on its own
// goroutine and may still be writing when the database closes. Both paths exit
// 0 either way: the process is stopping, and a non-zero status is what a
// service manager counts as a crash.
func reportStopped(loopsStopped, answersCut bool) {
	if !loopsStopped || answersCut {
		slog.Error("stopped without the final request-log write",
			"loops_stopped", loopsStopped, "answers_cut", answersCut)
		return
	}
	slog.Info("stopped")
}

// janitorGrace bounds the wait in waitForJanitor. It is a variable only so a
// test can shorten it.
var janitorGrace = 10 * time.Second

// stopBackgroundThenClose cancels the background loops, waits for them to
// return, and then closes the database. The request logger's final write begins
// after cancellation, so a database closed before the loops have returned fails
// that write and drops the last requests of the run. The close happens even
// when the wait times out: the process is stopping either way, and a stuck loop
// must not hold it open. It reports whether the loops returned in time.
//
// The scheduled backups are among the loops: a backup's final status is written
// after cancellation, the same way the request logger's last batch is.
func stopBackgroundThenClose(stop context.CancelFunc, done <-chan struct{}, closeDB func() error) bool {
	finished := waitForJanitor(stop, done)
	if err := closeDB(); err != nil {
		slog.Warn("close database", "error", err)
	}
	return finished
}

// waitForJanitor cancels the background loops and waits for them to return.
// The wait is bounded so that a loop which is stuck cannot hold the process
// open after the operator has asked it to stop. It reports whether the loops
// returned in time.
func waitForJanitor(stop context.CancelFunc, done <-chan struct{}) bool {
	stop()
	select {
	case <-done:
		return true
	case <-time.After(janitorGrace):
		slog.Warn("background loops did not stop in time")
		return false
	}
}

// shutdownHTTP stops srv and lets in-flight requests finish until ctx ends.
//
// Reaching that deadline is the stop the grace period was set to impose, not
// a failure: an answer still streaming when the process is told to stop is
// cut there. Returning it as an error reported every such restart as a failed
// start, with a failure exit status a service manager counts as a crash.
func shutdownHTTP(ctx context.Context, srv *http.Server) error {
	err := srv.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("shutdown deadline reached; in-flight answers were cut", "error", err)
		return nil
	}
	return err
}

func setupLogging(cfg config.Config) {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Dev {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}
