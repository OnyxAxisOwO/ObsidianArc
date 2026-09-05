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
	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/backup"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/compat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
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
	mailer := mail.New(mail.Config{
		Host:        cfg.Mail.Host,
		Port:        cfg.Mail.Port,
		Username:    cfg.Mail.Username,
		Password:    cfg.Mail.Password,
		From:        cfg.Mail.From,
		ImplicitTLS: cfg.Mail.ImplicitTLS,
		PublicURL:   cfg.Mail.PublicURL,
	})
	authService := auth.NewService(db, users, groups, settingsService, mailer, cfg)

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
	keys := apikey.NewStore(db)
	quotaService := quota.NewService(db, quota.NewStore(db), settingsService)
	chatService := chat.NewService(db, conversations, models, registry, settingsService)

	// The one check that decides whether an account may spend anything, and
	// the release that undoes what it claimed. Shared with the API surface
	// rather than written twice: two copies of a limit are two limits.
	guard := func(ctx context.Context, account user.User, chosen model.Model) (func(), error) {
		// An unconfirmed address is checked here rather than at sign-in: the
		// point is that it must not spend anything, and locking someone out
		// of the interface entirely would leave them nowhere to press
		// resend from.
		if !account.EmailVerified && authService.VerificationRequired() {
			return nil, httpx.ForbiddenCode("email_unverified",
				"Confirm your email address before sending a message.")
		}

		// One account, a bounded number of open generations. Claimed before
		// the allowance so a refusal here costs nothing to undo.
		freeSlot, err := quotaService.Begin(account.ID)
		if err != nil {
			return nil, httpx.TooManyRequests("too_many_in_flight",
				"Too many answers are already being generated for this account.")
		}

		// The worst case, not nothing. Reserving it is what makes the
		// allowance hold while several turns are streaming at once; the
		// release below gives back the whole reservation, and the turn record
		// adds what the turn really cost. Both are deltas, so their order
		// does not matter.
		tokens, credits := chosen.WorstCase()
		reserved := quota.Estimate{Tokens: tokens, Credits: credits}

		if err := quotaService.Reserve(ctx, account, reserved); err != nil {
			freeSlot()
			if translated := quota.TranslateError(err); translated != nil {
				return nil, translated
			}
			return nil, err
		}

		userID := account.ID
		return func() {
			freeSlot()
			// Detached: the request context is cancelled the moment the
			// browser goes away, and a reservation that is never given back
			// is an allowance quietly lost until the window rolls over.
			refundCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			if err := quotaService.Release(refundCtx, userID, reserved); err != nil {
				slog.ErrorContext(refundCtx, "could not release quota reservation",
					"error", err, "user", userID)
			}
		}, nil
	}

	// The gateway calls out to accounting rather than importing it: the chat
	// path stays readable, and usage can be swapped or disabled without the
	// gateway knowing.
	chatService.Authorize = func(
		ctx context.Context, req chat.TurnRequest, resolved model.Resolved,
	) (chat.Release, error) {
		return guard(ctx, req.User, resolved.Model)
	}
	recordTurn := func(ctx context.Context, record chat.TurnRecord) {
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

		// What the turn actually cost, added on top. The reservation taken
		// before it started is given back separately by the release, so this
		// is a plain addition and the two can happen in either order.
		actual := quota.Estimate{Tokens: int64(record.Usage.Total()), Credits: record.Credits}
		if err := quotaService.Settle(ctx, record.User.ID, quota.Estimate{}, actual); err != nil {
			slog.ErrorContext(ctx, "could not settle quota", "error", err, "user", record.User.ID)
		}
	}
	chatService.OnTurn = recordTurn

	if err := Bootstrap(ctx, db, groups, users, authService, cfg); err != nil {
		return nil, err
	}

	// Who may claim a forwarded address. Built once and shared, so the login
	// limiter and the trial budget cannot disagree about who is calling.
	proxyTrust, err := httpx.NewProxyTrust(cfg.TrustProxy, cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	if cfg.TrustProxy && len(cfg.TrustedProxies) == 0 {
		slog.WarnContext(ctx, "trusting forwarded headers from any private address; "+
			"set OBSIDIAN_TRUSTED_PROXIES to the proxy's address if it is reachable directly")
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

	auth.NewHandlers(authService, users, groups, preferences, settingsService, proxyTrust).Routes(mux)
	model.NewHandlers(models).Routes(mux)
	chatHandlers := chat.NewHandlers(chatService, conversations)
	// The one condition that must hold for an account to spend anything,
	// shared by the turn and by the upload that precedes it.
	chatHandlers.Uploadable = func(_ context.Context, account user.User) error {
		if !account.EmailVerified && authService.VerificationRequired() {
			return httpx.ForbiddenCode("email_unverified",
				"Confirm your email address first.")
		}
		return nil
	}
	// Read per request rather than captured, so raising the limit takes
	// effect without a restart.
	chatHandlers.MaxUploadBytes = func() int64 {
		return int64(settingsService.Int(settings.AttachmentMaxMB, 6)) * 1024 * 1024
	}
	chatHandlers.Routes(mux)
	quota.NewHandlers(quotaService).Routes(mux)

	// Programmatic access. The key store is what an account manages from the
	// interface; the compatibility surface is what the key is then presented
	// to, and the two are separate because one is a browser screen and the
	// other is not a browser at all.
	apiKeyHandlers := apikey.NewHandlers(keys)
	apiKeyHandlers.Allowed = func(r *http.Request) error {
		account, ok := auth.UserFrom(r.Context())
		if !ok {
			return httpx.Unauthorized("Sign in to continue.")
		}
		return apiAllowed(r.Context(), settingsService, groups, account)
	}
	apiKeyHandlers.Routes(mux)

	// An account's own data, in and out as one document. Separate from the
	// admin surface because it is the user's copy of their own history, not
	// the operator's copy of the instance.
	backup.NewHandlers(backup.NewService(db, conversations, preferences)).Routes(mux)

	compatHandlers := compat.NewHandlers(settingsService, users, groups, models, keys, registry)
	compatHandlers.Guard = guard
	compatHandlers.OnTurn = recordTurn
	compatHandlers.Routes(mux)
	announcement.NewHandlers(announcements).Routes(mux)
	trial.NewHandlers(settingsService, models, registry, proxyTrust, cfg.SecretKey).Routes(mux)
	admin.NewHandlers(users, groups, providers, models, settingsService, registry, authService, usageStore, quotaService, conversations, announcements, keys).Routes(mux)

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

// apiAllowed reports whether this account may use the API: the instance-wide
// switch, then the grant on their group. Administrators bypass the second the
// way they bypass every other group restriction, but not the first — a
// disabled API is disabled for everyone.
//
// It backs the interface's "you cannot create a key" state. The /v1 surface
// checks the same two conditions itself rather than calling this, because a
// screen wants to know why and a stranger with a token must not be told.
func apiAllowed(
	ctx context.Context, set *settings.Service, groups *group.Store, account user.User,
) error {
	if !set.Bool(settings.APIEnabled) {
		return httpx.ForbiddenCode("api_disabled", "The API is not enabled on this instance.")
	}
	if account.IsAdmin() {
		return nil
	}
	membership, err := groups.ByID(ctx, nil, account.GroupID)
	if err != nil || !membership.APIAccess {
		return httpx.ForbiddenCode("api_not_permitted",
			"Your group does not have API access.")
	}
	return nil
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
