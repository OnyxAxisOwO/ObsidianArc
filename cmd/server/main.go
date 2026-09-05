// Command server is the whole of Obsidian Arc: one process serving the API
// and the frontend, talking to one database.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server"
)

// Overridden at release time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
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

	// Cancelled by the shutdown path below, which is what stops the janitor.
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
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	serveErr := make(chan error, 1)
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
