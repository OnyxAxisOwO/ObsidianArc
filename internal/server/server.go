// Package server assembles the modules into one http.Handler.
//
// It is the only place that knows the whole route table, which is what keeps
// every other module free of routing decisions: a module exposes handlers,
// this file decides where they live and what middleware they sit behind.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/announcement"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/trial"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
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
	deps          Deps
	handler       http.Handler
	settings      *settings.Service
	auth          *auth.Service
	conversations *conversation.Store
	quota         *quota.Service
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

	// Provider API keys are encrypted with a key derived from the instance
	// secret; the box is the only thing that can read them back.
	box, err := secret.New(cfg.SecretKey, secret.PurposeProviderKey)
	if err != nil {
		return nil, err
	}
	providers := provider.NewStore(db, box)
	models := model.NewStore(db, providers)
	registry := adapter.NewRegistry(cfg.Upstream)
	conversations := conversation.NewStore(db)
	announcements := announcement.NewStore(db)
	usageStore := usage.NewStore(db)
	quotaService := quota.NewService(db, quota.NewStore(db), settingsService)
	chatService := chat.NewService(db, conversations, models, registry, settingsService)

	// The gateway calls out to accounting rather than importing it: the chat
	// path stays readable, and usage can be swapped or disabled without the
	// gateway knowing.
	chatService.Authorize = func(ctx context.Context, req chat.TurnRequest) error {
		if err := quotaService.Reserve(ctx, req.User); err != nil {
			if translated := quota.TranslateError(err); translated != nil {
				return translated
			}
			return err
		}
		return nil
	}
	chatService.OnTurn = func(ctx context.Context, record chat.TurnRecord) {
		if err := usageStore.Write(ctx, usage.Record{
			UserID:          record.User.ID,
			GroupID:         record.User.GroupID,
			ProviderID:      record.ProviderID,
			ProviderName:    record.ProviderName,
			ModelID:         record.Model.ID,
			ModelName:       record.Model.DisplayName,
			ModelRef:        record.Model.ModelID,
			ConversationID:  record.ConversationID,
			MessageID:       record.MessageID,
			RequestID:       record.RequestID,
			InputTokens:     record.Usage.InputTokens,
			OutputTokens:    record.Usage.OutputTokens,
			ReasoningTokens: record.Usage.ReasoningTokens,
			Credits:         record.Credits,
			Status:          usage.Status(record.Status),
			ErrorCode:       record.ErrorCode,
			StartedAt:       record.StartedAt.UnixMilli(),
			FinishedAt:      record.FinishedAt.UnixMilli(),
		}); err != nil {
			slog.ErrorContext(ctx, "could not record usage", "error", err, "user", record.User.ID)
		}

		// The request itself was already counted at reservation; this adds
		// what it turned out to cost.
		tokens := int64(record.Usage.Total())
		if err := quotaService.Settle(ctx, record.User.ID, tokens, record.Credits); err != nil {
			slog.ErrorContext(ctx, "could not settle quota", "error", err, "user", record.User.ID)
		}
	}

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
	model.NewHandlers(models).Routes(mux)
	chat.NewHandlers(chatService, conversations).Routes(mux)
	quota.NewHandlers(quotaService).Routes(mux)
	announcement.NewHandlers(announcements).Routes(mux)
	trial.NewHandlers(settingsService, models, registry, cfg.TrustProxy).Routes(mux)
	admin.NewHandlers(users, groups, providers, models, settingsService, registry, authService, usageStore, quotaService, conversations, announcements).Routes(mux)

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

	return &Server{
		deps:          deps,
		handler:       handler,
		settings:      settingsService,
		auth:          authService,
		conversations: conversations,
		quota:         quotaService,
	}, nil
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
	// Expired sessions are already refused on read; the sweep is only about
	// not letting the table grow forever.
	_, _ = s.auth.Sessions().DeleteExpired(sweepCtx)
	// Images uploaded into a composer that was never sent.
	_, _ = s.conversations.DeleteOrphans(sweepCtx, conversation.OrphanTTL)
	// Counter buckets whose window has long since rolled over. The ledger is
	// never pruned: it is the audit trail.
	_, _ = s.quota.PruneCounters(sweepCtx)
}

func devServerURL(cfg config.Config) string {
	if !cfg.Dev {
		return ""
	}
	return "http://127.0.0.1:5173"
}
