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
	"syscall"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/systembackup"
)

// The build's identity, stamped in by the Makefile as
// `-ldflags "-X main.version=vyyyy.MM.dd.HH.mm.ss"` — the UTC moment it was
// compiled. Zero-padded, so it sorts chronologically as plain text, and
// unique per build,
// so "which build is this server running" is answerable from /api/health
// without a tag or a counter to keep up to date.
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *archivePath == "" || flags.NArg() != 0 {
		return errors.New("usage: obsidian-arc restore-backup --file <archive.arcbackup>")
	}
	if err := requireExistingRestoreKey(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := systembackup.VerifyArchiveKey(*archivePath, cfg.SecretKey); err != nil {
		return err
	}
	setupLogging(cfg)
	ctx := context.Background()
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		return err
	}
	return systembackup.RestoreArchive(ctx, db, *archivePath, cfg.SecretKey)
}

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
	if err != nil || len(strings.TrimSpace(string(key))) < 16 {
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
			"file", cfg.DataDir+string(os.PathSeparator)+config.SecretKeyFile)
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
	defer db.Close()

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

	// Cancelled by the shutdown path below, which signals the background loops.
	backgroundCtx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	app.StartJanitor(backgroundCtx)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: app.Handler(),
		// No WriteTimeout: a streamed answer legitimately takes minutes, and
		// a global write deadline would sever it mid-sentence. Streaming
		// handlers set their own deadlines through http.ResponseController.
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
	if err := srv.Shutdown(stopCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	slog.Info("stopped")
	return nil
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
