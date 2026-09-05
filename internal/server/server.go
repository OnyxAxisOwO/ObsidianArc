// Package server assembles the modules into one http.Handler.
//
// It is the only place that knows the whole route table, which is what keeps
// every other module free of routing decisions: a module exposes handlers,
// this file decides where they live and what middleware they sit behind.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/web"
)

type Deps struct {
	Config  config.Config
	DB      *database.DB
	Version string
	Started time.Time
}

// Server is the assembled application. It owns the modules so that background
// work (the janitor) and the HTTP handler share one set of stores rather than
// each constructing its own.
type Server struct {
	deps     Deps
	handler  http.Handler
	settings *settings.Service
	auth     *auth.Service
}

func New(ctx context.Context, deps Deps) (*Server, error) {
	cfg := deps.Config
	db := deps.DB

	// Modules, wired bottom-up. Each takes what it needs and nothing else, so
	// the dependency direction is visible here rather than inferred from
	// imports scattered across packages.
	settingsService := settings.New(db)
	if err := settingsService.Load(ctx); err != nil {
		return nil, err
	}

	groups := group.NewStore(db)
	users := user.NewStore(db)
	preferences := user.NewPreferenceStore(db)
	authService := auth.NewService(db, users, groups, settingsService, cfg)

	if err := Bootstrap(ctx, db, groups, users, authService, cfg); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		if err := db.Pool().PingContext(r.Context()); err != nil {
			return httpx.Unavailable("Database is not reachable.").WithCause(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"version":    deps.Version,
			"uptime_sec": int64(time.Since(deps.Started).Seconds()),
		})
	}))

	auth.NewHandlers(authService, users, groups, preferences, settingsService, cfg.TrustProxy).Routes(mux)

	// Anything under /api that no module claimed is a client bug, and should
	// read as one instead of quietly returning the SPA shell.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NotFound("No such endpoint."))
	})

	frontend, err := web.Handler(web.Options{
		Dev:       cfg.Dev,
		DevServer: devServerURL(cfg),
	})
	if err != nil {
		return nil, err
	}
	mux.Handle("/", frontend)

	handler := httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Recover(),
		httpx.Logger(),
		httpx.SecurityHeaders(cfg.Dev, web.InlineScriptHashes()),
		httpx.SameOrigin(cfg.AllowedOrigins),
		// Last, so the session lookup only happens for requests that survived
		// the origin check.
		authService.Attach(),
	)

	return &Server{deps: deps, handler: handler, settings: settingsService, auth: authService}, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

// StartJanitor runs the one piece of periodic work this server has: expiring
// sessions. It is a single goroutine on a ticker, not a scheduler, and it
// stops when the context does.
func (s *Server) StartJanitor(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			// Run once at start too: a process that was down over a weekend
			// should not wait ten more minutes to clean up.
			s.sweep(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) sweep(ctx context.Context) {
	sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := s.auth.Sessions().DeleteExpired(sweepCtx); err != nil {
		// A failed sweep is not worth interrupting service over: the rows are
		// already treated as expired on read.
		return
	}
}

func devServerURL(cfg config.Config) string {
	if !cfg.Dev {
		return ""
	}
	return "http://127.0.0.1:5173"
}
