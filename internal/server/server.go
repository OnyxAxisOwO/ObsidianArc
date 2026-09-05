// Package server assembles the modules into one http.Handler.
//
// It is the only place that knows the whole route table, which is what keeps
// every other module free of routing decisions: a module exposes handlers,
// this file decides where they live and what middleware they sit behind.
package server

import (
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/web"
)

type Deps struct {
	Config  config.Config
	DB      *database.DB
	Version string
	Started time.Time
}

func New(deps Deps) (http.Handler, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		if err := deps.DB.Pool().PingContext(r.Context()); err != nil {
			return httpx.Unavailable("Database is not reachable.").WithCause(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"version":    deps.Version,
			"uptime_sec": int64(time.Since(deps.Started).Seconds()),
		})
	}))

	// Anything under /api that no module claimed is a client bug, and should
	// read as one instead of quietly returning the SPA shell.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NotFound("No such endpoint."))
	})

	frontend, err := web.Handler(web.Options{
		Dev:       deps.Config.Dev,
		DevServer: devServerURL(deps.Config),
	})
	if err != nil {
		return nil, err
	}
	mux.Handle("/", frontend)

	return httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Recover(),
		httpx.Logger(),
		httpx.SecurityHeaders(deps.Config.Dev, web.InlineScriptHashes()),
		httpx.SameOrigin(deps.Config.AllowedOrigins),
	), nil
}

func devServerURL(cfg config.Config) string {
	if !cfg.Dev {
		return ""
	}
	return "http://127.0.0.1:5173"
}
