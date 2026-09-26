// Package server assembles the modules into one http.Handler.
//
// It is the only place that knows the whole route table, which is what keeps
// every other module free of routing decisions: a module exposes handlers,
// this file decides where they live and what middleware they sit behind.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/agent"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/announcement"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/backup"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/compat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/consolessh"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/feedback"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/idp"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/project"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/screening"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/trial"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
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
	users         *user.Store
	conversations *conversation.Store
	quota         *quota.Service
	requests      *reqlog.Store
	idp           *idp.Store
	notify        *notify.Store
	invites       *invite.Store
	health        *health.Checker
	// nil when no SSH address is configured, which is the default.
	ssh *consolessh.Server
	// What the console dispatches into: the API with no Attach in front of
	// it, which is how a command arrives over SSH. Kept so a test can send a
	// request the way SSH does, without a session of its own.
	consoleAPI http.Handler
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
	healthStore := health.NewStore(db)
	conversations := conversation.NewStore(db)
	announcements := announcement.NewStore(db)
	feedbackStore := feedback.NewStore(db)
	notifyStore := notify.NewStore(db)
	// Set on every store that raises one, rather than threaded through each
	// constructor: nil elsewhere would mean "wire this one later and hope
	// nothing calls it before then", and every store already treats a nil
	// Notify as "push nothing" for its own tests.
	feedbackStore.Notify = notifyStore
	announcements.Notify = notifyStore
	usageStore := usage.NewStore(db)
	requestLog := reqlog.NewStore(db)
	securityLog := securityevents.NewStore(db)
	keys := apikey.NewStore(db)
	cards := card.NewStore(db)
	invites := invite.NewStore(db, users, cards, groups, settingsService)
	invites.Notify = notifyStore
	quotaService := quota.NewService(db, quota.NewStore(db), settingsService)
	projects := project.NewStore(db)

	chatService := chat.NewService(db, conversations, models, registry, settingsService)
	// Scoped by owner inside the store, so a conversation carrying somebody
	// else's project id reads back nothing rather than their brief.
	chatService.ProjectInstructions = func(ctx context.Context, actor user.User, projectID string) string {
		record, err := projects.Get(ctx, nil, actor.ID, projectID)
		if err != nil {
			return ""
		}
		return record.Instructions
	}

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
		// the allowance so a refusal here costs nothing to undo. Administrators
		// are exempt from concurrency caps; for regular accounts, the ceiling
		// is operator-configurable (<= 0 means unlimited).
		maxConcurrent := settingsService.Int(settings.QuotaMaxConcurrent, quota.DefaultMaxConcurrent)
		if account.IsAdmin() {
			maxConcurrent = 0
		}
		freeSlot, err := quotaService.Begin(account.ID, maxConcurrent)
		if err != nil {
			return nil, httpx.TooManyRequests("too_many_in_flight",
				"Too many answers are already being generated for this account.")
		}

		// The worst case, not nothing. Reserving it is what makes the
		// allowance hold while several turns are streaming at once; the
		// release below gives back the whole reservation, and the turn record
		// adds what the turn really cost. Both are deltas, so their order
		// does not matter — but which counter each lands in does, so the
		// reservation carries the moment it was charged and is given back
		// there rather than wherever the answer happened to finish.
		tokens, credits := chosen.WorstCase()
		estimate := quota.Estimate{Tokens: tokens, Credits: credits}
		autoReset, err := preferences.Bool(ctx, account.ID, user.AutoUseResetCard)
		if err != nil {
			freeSlot()
			return nil, err
		}

		var reserved quota.Reservation
		if autoReset {
			reserved, err = quotaService.ReserveWithAutoReset(ctx, account, estimate,
				func(ctx context.Context, q database.Queryer, needed quota.Window) ([]string, bool, error) {
					spentCard, err := cards.SpendNextForWindow(ctx, q, account.ID, string(needed))
					if errors.Is(err, card.ErrNotFound) {
						return nil, false, nil
					}
					if err != nil {
						return nil, false, err
					}
					return spentCard.Windows, true, nil
				})
		} else {
			reserved, err = quotaService.Reserve(ctx, account, estimate)
		}
		if err != nil {
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
		if err := users.MarkActive(ctx, record.User.ID, record.FinishedAt.UnixMilli()); err != nil {
			slog.ErrorContext(ctx, "could not record account activity", "error", err, "user", record.User.ID)
		}
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
			Estimated:       record.Usage.Estimated,
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
		if err := quotaService.Settle(ctx, record.User, quota.Estimate{}, actual); err != nil {
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
	authService.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
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

	authHandlers := auth.NewHandlers(authService, users, groups, preferences, settingsService, proxyTrust)
	authHandlers.Routes(mux)
	modelHandlers := model.NewHandlers(models)
	// What readers are told about liveness, decided here because it is the
	// operator's policy and neither the model package nor the health one has
	// any business reading settings.
	//
	// Cached for half a minute: this runs on every model listing, which the
	// chat asks for on load, and the underlying figure moves on a ten-minute
	// sweep. Thirty seconds is far fresher than the data behind it.
	var (
		livenessMu   sync.Mutex
		livenessAt   time.Time
		livenessRev  uint64
		livenessSeen map[string]model.Liveness
	)
	modelHandlers.Liveness = func(ctx context.Context) map[string]model.Liveness {
		show := settingsService.Bool(settings.HealthShowUsers)
		warnBelow := settingsService.Int(settings.HealthWarnBelow, 0)
		if !show && warnBelow <= 0 {
			return nil
		}

		livenessMu.Lock()
		defer livenessMu.Unlock()
		revision := healthStore.Revision()
		if time.Since(livenessAt) < 30*time.Second && livenessRev == revision {
			return livenessSeen
		}

		window := time.Duration(settingsService.Int(settings.HealthWindowMins, 30)) * time.Minute
		// A reader's window is the day, not the sweep's: "unstable" should
		// mean the model has been unreliable, not that it missed once in the
		// last half hour.
		if window < 24*time.Hour {
			window = 24 * time.Hour
		}
		since := time.Now().Add(-window).UnixMilli()
		if resetAt := int64(settingsService.Int(settings.HealthResetAt, 0)); resetAt > since {
			since = resetAt
		}
		rates, err := healthStore.Rates(ctx, since)
		if err != nil {
			slog.ErrorContext(ctx, "liveness for readers", "error", err)
			return livenessSeen
		}

		seen := make(map[string]model.Liveness, len(rates))
		for modelID, rate := range rates {
			// Both the published figure and its configured warning describe
			// observed evidence, so one real sample is enough. Automatic
			// disabling keeps its separate evidence floor: that action changes
			// availability rather than merely telling readers what was observed.
			share := rate.Share()
			entry := model.Liveness{
				Unstable: warnBelow > 0 && share*100 < float64(warnBelow),
			}
			if show {
				value := share
				entry.Uptime = &value
			}
			seen[modelID] = entry
		}
		livenessSeen, livenessAt, livenessRev = seen, time.Now(), revision
		return seen
	}
	modelHandlers.Routes(mux)

	mux.HandleFunc("GET /api/uptime", httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		account, ok := auth.UserFrom(r.Context())
		if !ok {
			return httpx.Unauthorized("Sign in to view uptime.")
		}
		show := settingsService.Bool(settings.HealthShowUsers)
		if !account.CanAdmin("availability") && !show {
			return httpx.Forbidden("Uptime is not visible to users.")
		}

		resetAt := int64(settingsService.Int(settings.HealthResetAt, 0))
		// The card compares the last hour with a fixed day of history, so
		// changing the probe policy must not change either displayed window.
		now := time.Now()
		windowStart := now.Add(-24 * time.Hour).UnixMilli()
		since := windowStart
		if resetAt > since {
			since = resetAt
		}
		rates, err := healthStore.Rates(r.Context(), since)
		if err != nil {
			return httpx.Internal(err)
		}
		hourSince := max(now.Add(-time.Hour).UnixMilli(), resetAt)
		hourRates, err := healthStore.Rates(r.Context(), hourSince)
		if err != nil {
			return httpx.Internal(err)
		}
		timeline, err := healthStore.Timeline(r.Context(), windowStart, resetAt, 24)
		if err != nil {
			return httpx.Internal(err)
		}

		var (
			modelList []model.Model
		)
		if account.CanAdmin("availability") {
			modelList, err = models.ListAll(r.Context(), "")
		} else {
			modelList, err = models.ListForUser(r.Context(), account.GroupID, false)
		}
		if err != nil {
			return httpx.Internal(err)
		}

		type modelUptimeItem struct {
			ID          string             `json:"id"`
			DisplayName string             `json:"display_name"`
			Provider    string             `json:"provider_name,omitempty"`
			Enabled     bool               `json:"enabled"`
			Uptime      *float64           `json:"uptime,omitempty"`
			UptimeHour  *float64           `json:"uptime_hour,omitempty"`
			State       string             `json:"state"`
			Total       int                `json:"total"`
			History     []health.TimePoint `json:"history"`
		}

		warnBelow := settingsService.Int(settings.HealthWarnBelow, 0)
		result := make([]modelUptimeItem, 0, len(modelList))
		for _, m := range modelList {
			if account.CanAdmin("availability") && !m.Enabled && !m.AutoDisabled {
				continue
			}
			rate, hasRate := rates[m.ID]
			item := modelUptimeItem{
				ID:          m.ID,
				DisplayName: m.DisplayName,
				Enabled:     m.Enabled,
				State:       "unknown",
			}
			if account.CanAdmin("availability") {
				item.Provider = m.ProviderName
			}
			if m.AutoDisabled {
				item.State = "down"
			}
			if hasRate && rate.Total > 0 {
				share := rate.Share()
				item.Uptime = &share
				item.Total = rate.Total
			}
			// The card's status badge ("operational", "degraded", "outage")
			// evaluates against the recent one-hour window matching the hourly
			// percentage shown beside it, rather than the 24-hour aggregate.
			if hourRate, hasHour := hourRates[m.ID]; hasHour && hourRate.Total > 0 {
				share := hourRate.Share()
				item.UptimeHour = &share
				if m.AutoDisabled {
					item.State = "down"
				} else if hourRate.OK == 0 {
					// With recent evidence present, zero answers is an outage rather
					// than degraded service.
					item.State = "down"
				} else if share >= 0.99 {
					item.State = "up"
				} else if (warnBelow > 0 && share*100 < float64(warnBelow)) || (warnBelow <= 0 && share < 0.95) {
					item.State = "degraded"
				} else {
					item.State = "up"
				}
			}

			pts, hasPts := timeline[m.ID]
			if !hasPts || len(pts) == 0 {
				span := (now.UnixMilli() - windowStart) / 24
				if span <= 0 {
					span = 1
				}
				pts = make([]health.TimePoint, 24)
				for i := 0; i < 24; i++ {
					pts[i] = health.TimePoint{At: windowStart + int64(i)*span + span/2, Total: 0}
				}
			}
			item.History = pts

			result = append(result, item)
		}

		uptimeStart := deps.Started
		if resetAt > 0 && resetAt > uptimeStart.UnixMilli() {
			uptimeStart = time.UnixMilli(resetAt)
		}
		sec := int64(time.Since(uptimeStart).Seconds())
		if sec < 0 {
			sec = 0
		}

		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"uptime_sec": sec,
			"models":     result,
		})
	}))
	// One client for every Turnstile check, so registration, key creation and
	// a burst challenge reuse connections to Cloudflare.
	challengeClient := &http.Client{}
	chatHandlers := chat.NewHandlers(chatService, conversations)
	// Scoped by owner, so somebody else'''s project is reported as absent
	// rather than as forbidden — which is also all the query knows.
	chatHandlers.ProjectAllowed = func(ctx context.Context, actor user.User, projectID string) error {
		if _, err := projects.Get(ctx, nil, actor.ID, projectID); err != nil {
			return httpx.NotFound("No such project.")
		}
		return nil
	}
	// The one condition that must hold for an account to spend anything,
	// shared by the turn and by the upload that precedes it.
	chatHandlers.Uploadable = func(_ context.Context, account user.User) error {
		if !account.EmailVerified && authService.VerificationRequired() {
			return httpx.ForbiddenCode("email_unverified",
				"Confirm your email address first.")
		}
		return nil
	}
	chatHandlers.Deletable = func(ctx context.Context, account user.User) error {
		return deleteAllowed(ctx, groups, account)
	}
	chatHandlers.Archivable = func(ctx context.Context, account user.User) error {
		return archiveAllowed(ctx, settingsService, account)
	}
	// Read per request rather than captured, so raising the limit takes
	// effect without a restart.
	chatHandlers.MaxUploadBytes = func() int64 {
		return int64(settingsService.Int(settings.AttachmentMaxMB, 6)) * 1024 * 1024
	}
	chatHandlers.Security = securityLog
	chatHandlers.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
	chatHandlers.ChatChallenge = turnstile.Gate{
		Client: challengeClient,
		Enabled: func() bool {
			return settingsService.Int(settings.ChatChallengeRequests, 0) > 0
		},
		Secret: func() string { return settingsService.Get(settings.TurnstileSecretKey) },
	}
	chatHandlers.ChatChallengePolicy = func() securityevents.ChatPolicy {
		return securityevents.ChatPolicy{
			Requests: settingsService.Int(settings.ChatChallengeRequests, 0),
			Window: time.Duration(
				settingsService.Int(settings.ChatChallengeWindowSecs, 60)) * time.Second,
			Clearance: time.Duration(
				settingsService.Int(settings.ChatChallengeClearMins, 30)) * time.Minute,
		}
	}
	chatHandlers.Routes(mux)
	quotaHandlers := quota.NewHandlers(quotaService)
	quotaHandlers.Routes(mux)
	usageHandlers := usage.NewHandlers(usageStore)
	usageHandlers.Routes(mux)

	cardHandlers := card.NewHandlers(cards)
	// What spending a card actually buys. The card package does not know the
	// counters exist; this is the one line that connects the two.
	cardHandlers.OnSpend = func(ctx context.Context, account user.User, c card.Card) error {
		return quotaService.ResetWindows(ctx, []string{account.ID}, c.Windows)
	}
	// So the limit on guessing at redemption codes counts one host's attempts
	// together, not just one account's.
	cardHandlers.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
	cardHandlers.Challenge = turnstile.Gate{
		Client:  challengeClient,
		Enabled: func() bool { return settingsService.Bool(settings.TurnstileOnRedeem) },
		Secret:  func() string { return settingsService.Get(settings.TurnstileSecretKey) },
	}
	cardHandlers.Routes(mux)

	// An account's own invite code and who has used it. Behind
	// auth.RequireUser alone, like notify's and card's own account-facing
	// routes — see internal/invite/http.go.
	inviteHandlers := invite.NewHandlers(invites)
	inviteHandlers.Routes(mux)

	// Programmatic access. The key store is what an account manages from the
	// interface; the compatibility surface is what the key is then presented
	// to, and the two are separate because one is a browser screen and the
	// other is not a browser at all.
	authService.Challenge = turnstile.Gate{
		Client:  challengeClient,
		Enabled: func() bool { return settingsService.Bool(settings.TurnstileOnSignup) },
		Secret:  func() string { return settingsService.Get(settings.TurnstileSecretKey) },
	}
	authService.LoginChallenge = turnstile.Gate{
		Client:  challengeClient,
		Enabled: func() bool { return settingsService.Bool(settings.TurnstileOnLogin) },
		Secret:  func() string { return settingsService.Get(settings.TurnstileSecretKey) },
	}

	// Asking a model whether a sign-up looks like a person. A restriction is
	// the middle answer: the account exists and can use the website, but the
	// programmatic surface stays closed until the configured time passes.
	reviewer := screening.Reviewer{
		Registry: registry,
		Resolve: func(ctx context.Context) (adapter.Provider, adapter.ModelSpec, error) {
			record, err := models.ByID(ctx, settingsService.Get(settings.SignupReviewModel))
			if err != nil {
				return adapter.Provider{}, adapter.ModelSpec{}, err
			}
			upstream, err := providers.Resolve(ctx, record.ProviderID)
			if err != nil {
				return adapter.Provider{}, adapter.ModelSpec{}, err
			}
			return upstream, record.Spec(), nil
		},
	}
	authService.ReviewSignup = func(ctx context.Context, in auth.RegisterInput, fromAddress int) (auth.SignupReview, error) {
		if !settingsService.Bool(settings.SignupReview) ||
			settingsService.Get(settings.SignupReviewModel) == "" {
			return auth.SignupReview{Decision: auth.SignupAllow}, nil
		}

		mode := screening.ParseMode(settingsService.Get(settings.SignupReviewMode))
		verdict, err := reviewer.Review(ctx, mode, screening.Facts{
			Username: in.Username, Email: in.Email, QQ: in.QQ, Nickname: in.Nickname,
			IP: in.IP, UserAgent: in.UA, FromThisAddress: fromAddress,
		})
		if err != nil {
			// The fallback is part of the verdict: loose allows, normal restricts,
			// and strict refuses. The failure is still logged because an operator
			// must be able to distinguish policy from a broken reviewer.
			slog.WarnContext(ctx, "signup review could not answer",
				"username", in.Username, "mode", mode, "decision", verdict.Decision, "error", err)
		}

		out := auth.SignupReview{
			Ran: true, Decision: auth.SignupDecision(verdict.Decision), Reason: verdict.Reason,
		}
		if verdict.Decision == screening.DecisionRestrict {
			hours := settingsService.Int(settings.SignupReviewRestrictHours, 24)
			if hours > 0 {
				out.RestrictedUntil = time.Now().Add(time.Duration(hours) * time.Hour).UnixMilli()
			}
		}
		return out, err
	}
	// Switching the second step on or off and spending a recovery code are
	// what somebody asks about after an account has been taken over, so they
	// are kept where the other decisions about who gets in are.
	authService.OnTwoFactor = func(ctx context.Context, event auth.TwoFactorEvent) {
		severity := securityevents.SeverityInfo
		if event.Kind == "disabled" || event.Kind == "recovery_used" {
			severity = securityevents.SeverityWarning
		}
		if err := securityLog.Record(ctx, nil, securityevents.Event{
			Event: securityevents.EventTwoFactor, Severity: severity,
			UserID: event.Account.ID, Username: event.Account.Username,
			IP: event.IP, Source: "user", Decision: event.Kind,
		}); err != nil {
			slog.ErrorContext(ctx, "could not record a two-step change", "error", err)
		}
		// Told to the account too, in the bell: the change was made from a
		// signed-in session, and if that session was not its owner's, the
		// owner hears about it wherever they are signed in.
		if err := notifyStore.Push(ctx, nil, notify.Notification{
			Audience: notify.AudienceUser, UserID: event.Account.ID,
			Kind: "two_factor_changed", Params: map[string]any{"kind": event.Kind},
			Link: "/settings?tab=security",
		}); err != nil {
			slog.ErrorContext(ctx, "could not notify a two-step change", "error", err)
		}
	}
	// A sign-in from a device the account had not used before: logged where
	// the operator looks after a takeover, and told to the owner, who is the
	// one person who can say it was not them.
	authService.OnNewDevice = func(ctx context.Context, event auth.NewDeviceEvent) {
		if err := securityLog.Record(ctx, nil, securityevents.Event{
			Event: securityevents.EventNewDevice, Severity: securityevents.SeverityInfo,
			UserID: event.Account.ID, Username: event.Account.Username,
			IP: event.IP, Source: "user", Reason: event.UserAgent,
		}); err != nil {
			slog.ErrorContext(ctx, "could not record a new device", "error", err)
		}
		if err := notifyStore.Push(ctx, nil, notify.Notification{
			Audience: notify.AudienceUser, UserID: event.Account.ID,
			Kind:   "new_device_login",
			Params: map[string]any{"ip": event.IP, "ua": event.UserAgent},
			Link:   "/settings?tab=security",
		}); err != nil {
			slog.ErrorContext(ctx, "could not notify a new device", "error", err)
		}
	}
	// Invite codes. Three hooks rather than a dependency internal/auth
	// takes on internal/invite directly — see auth.InviteGrant's comment —
	// translating between that package's own Grant and auth's local
	// InviteGrant, which is otherwise identical.
	authService.ConsumeInvite = func(ctx context.Context, tx *database.Tx, code string) (*auth.InviteGrant, error) {
		grant, err := invites.Consume(ctx, tx, code, time.Now().UnixMilli())
		if err != nil {
			return nil, err
		}
		return &auth.InviteGrant{
			CodeID: grant.CodeID, OwnerID: grant.OwnerID, GroupID: grant.GroupID, GroupDays: grant.GroupDays,
		}, nil
	}
	authService.RecordInviteUse = func(ctx context.Context, tx *database.Tx, grant auth.InviteGrant, userID string) error {
		return invites.RecordUse(ctx, tx, grant.CodeID, userID, grant.OwnerID, grant.GroupDays)
	}
	authService.RewardInvite = func(ctx context.Context, userID string, verificationRequired bool) {
		if err := invites.Reward(ctx, userID, verificationRequired); err != nil {
			slog.ErrorContext(ctx, "could not resolve an invite reward", "error", err, "user", userID)
		}
	}
	authService.OnSignupReview = func(ctx context.Context, in auth.RegisterInput, account *user.User, review auth.SignupReview) {
		event := securityevents.Event{
			Event: securityevents.EventSignupReview, Severity: securityevents.SeverityInfo,
			Username: in.Username, IP: in.IP, Source: "ai", Decision: string(review.Decision),
			Reason: review.Reason,
		}
		if account != nil {
			event.UserID = account.ID
		}
		if review.Decision == auth.SignupRestrict {
			event.Severity = securityevents.SeverityWarning
		} else if review.Decision == auth.SignupRefuse {
			event.Severity = securityevents.SeverityDanger
		}
		if err := securityLog.Record(ctx, nil, event); err != nil {
			slog.ErrorContext(ctx, "could not record signup review", "error", err)
		}
		// Only what the reviewer held back: a notice for every approval
		// would be a notice per sign-up, and nobody reads those.
		if review.Decision == auth.SignupRestrict || review.Decision == auth.SignupRefuse {
			if err := notifyStore.Push(ctx, nil, notify.Notification{
				Audience: notify.AudienceAdmins, Permission: "security",
				Kind: "signup_flagged", Params: map[string]any{"username": in.Username},
				Link: "/admin/security",
			}); err != nil {
				slog.ErrorContext(ctx, "could not notify a flagged signup", "error", err)
			}
		}
	}

	adminTryReview := func(ctx context.Context, in admin.ReviewTrial) (string, string, error) {
		verdict, err := reviewer.Review(ctx,
			screening.ParseMode(settingsService.Get(settings.SignupReviewMode)),
			screening.Facts{
				Username: in.Username, Email: in.Email, QQ: in.QQ, Nickname: in.Nickname,
				UserAgent: in.UserAgent, FromThisAddress: in.FromThisAddress,
			})
		return string(verdict.Decision), verdict.Reason, err
	}

	apiKeyHandlers := apikey.NewHandlers(keys)
	apiKeyHandlers.Challenge = turnstile.Gate{
		Client:  challengeClient,
		Enabled: func() bool { return settingsService.Bool(settings.TurnstileOnAPIKey) },
		Secret:  func() string { return settingsService.Get(settings.TurnstileSecretKey) },
	}
	apiKeyHandlers.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
	apiKeyHandlers.Allowed = func(r *http.Request) error {
		account, ok := auth.UserFrom(r.Context())
		if !ok {
			return httpx.Unauthorized("Sign in to continue.")
		}
		return apiAllowed(r.Context(), settingsService, groups, account)
	}
	// Pinning a key to a model is checked against the same catalogue the turn
	// itself is checked against, so a key cannot be pinned to something its
	// owner could not have sent to anyway.
	apiKeyHandlers.ModelAllowed = func(r *http.Request, modelID string) error {
		account, ok := auth.UserFrom(r.Context())
		if !ok {
			return httpx.Unauthorized("Sign in to continue.")
		}
		_, err := models.Authorize(r.Context(), account.GroupID, modelID, account.IsAdmin())
		if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrNotPermitted) {
			return httpx.BadRequest("That model is not available to this account.")
		}
		if err != nil {
			return httpx.Internal(err)
		}
		return nil
	}
	apiKeyHandlers.Routes(mux)

	// An account's own data, in and out as one document. Separate from the
	// admin surface because it is the user's copy of their own history, not
	// the operator's copy of the instance.
	backupHandlers := backup.NewHandlers(backup.NewService(db, conversations, preferences))
	backupHandlers.Routes(mux)

	projectHandlers := project.NewHandlers(projects)
	projectHandlers.Routes(mux)

	compatHandlers := compat.NewHandlers(settingsService, users, groups, models, keys, registry)
	compatHandlers.Guard = guard
	compatHandlers.OnTurn = recordTurn
	compatHandlers.Routes(mux)
	announcement.NewHandlers(announcements).Routes(mux)
	feedbackHandlers := feedback.NewHandlers(feedbackStore)
	// Signed in and spending nothing, so this is the one challenge an
	// operator switches on after being spammed rather than before.
	feedbackHandlers.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
	feedbackHandlers.Challenge = turnstile.Gate{
		Client:  challengeClient,
		Enabled: func() bool { return settingsService.Bool(settings.TurnstileOnFeedback) },
		Secret:  func() string { return settingsService.Get(settings.TurnstileSecretKey) },
	}
	// Read per request rather than captured, so turning it off takes effect
	// on the next thread somebody opens rather than on the next restart.
	feedbackHandlers.ShowStaffName = func() bool {
		return settingsService.Bool(settings.FeedbackShowStaffName)
	}
	feedbackHandlers.Routes(mux)

	// Signing in with an account somebody already holds at GitHub or Google.
	// This instance is the client of those providers and never one itself:
	// nothing here issues an identity for anybody else to check.
	oauthService := oauth.NewService(db, oauth.NewStore(db), users, authService, settingsService)
	oauthHandlers := oauth.NewHandlers(oauthService, cfg.SecretKey)
	// Its own client rather than the challenge one above: these calls go to
	// two other hosts, and a pool per destination is what keeps a slow
	// provider from sitting in front of a Turnstile check.
	oauthHandlers.Client = &http.Client{}
	// The state cookie rides beside the session cookie and is marked the same
	// way, so a development instance over plain HTTP still works and a real
	// one never puts it on the wire in the clear.
	oauthHandlers.SecureCookie = cfg.Session.SecureCookie
	// TLS ends at the proxy in every deployment of this, so neither the host
	// nor the scheme can come from this process's own socket.
	publicOrigin := func(r *http.Request) string {
		return httpx.PublicOrigin(r, proxyTrust, cfg.Mail.PublicURL)
	}
	oauthHandlers.Origin = publicOrigin
	oauthHandlers.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
	oauthHandlers.Routes(mux)
	// Which buttons the sign-in card draws. Read per request, so switching a
	// provider on takes effect on the next visitor rather than the next
	// restart.
	authHandlers.SignInProviders = func() []auth.SignInProvider {
		out := []auth.SignInProvider{}
		for _, provider := range oauth.Providers() {
			if oauthService.Enabled(provider.ID) {
				out = append(out, auth.SignInProvider{ID: provider.ID, Name: provider.Name})
			}
		}
		return out
	}

	// And the other direction: this instance as the place somebody else's site
	// sends people to sign in. internal/idp is the provider; internal/oauth
	// above is the client. Two features, opposite arrows, and the package
	// comments on both say which is which.
	signingBox, err := secret.New(cfg.SecretKey, secret.PurposeSigningKey)
	if err != nil {
		return nil, err
	}
	idpStore := idp.NewStore(db)
	idpService := idp.NewService(idpStore, idp.NewKeys(db, signingBox), users, groups)
	idpHandlers := idp.NewHandlers(idpService, cfg.SecretKey)
	// The issuer named in every identity token, and in the discovery document
	// a client library reads to configure itself. The same resolution the
	// provider callbacks use, so the two cannot disagree about what this
	// instance is called.
	idpHandlers.Origin = publicOrigin
	idpHandlers.Routes(mux)

	trial.NewHandlers(settingsService, models, registry, proxyTrust, cfg.SecretKey).Routes(mux)
	adminHandlers := admin.NewHandlers(db, users, groups, providers, models, settingsService, registry, authService, usageStore, quotaService, conversations, announcements, keys, requestLog, securityLog, cards, healthStore, feedbackStore, idpStore, invites)
	adminHandlers.TryReview = adminTryReview
	adminHandlers.Origin = publicOrigin
	adminHandlers.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
	adminHandlers.Notify = notifyStore
	adminHandlers.Routes(mux)

	// The browser's own inbox. Behind auth.RequireUser alone — never the
	// console's mux, whose parity tests treat it as the account-facing route
	// it is (see consoleExempt in internal/console) — because what it shows
	// is computed per account rather than gated by an administrative grant.
	notify.NewHandlers(notifyStore).Routes(mux)

	// The console is a client of the administrative API, not a second
	// implementation of it. Its own mux carries the same handlers mounted the
	// same way, so a command reaching an endpoint passes auth.RequireAdmin
	// and that route's permission wrapper exactly as a browser request does.
	// That is the whole of the console's permission story, and it is why
	// there is no second permission table here to drift out of step with the
	// one above.
	consoleAPI := http.NewServeMux()
	adminHandlers.Routes(consoleAPI)
	// The same arrangement for the commands any signed-in account may run.
	// They act on the account's own settings, keys, conversations, usage and
	// export — the endpoints that account's own screens call — so those
	// handlers are mounted here on the same terms: the dispatched request
	// passes the real auth.RequireUser and the real owner scoping, and a
	// command still cannot reach anything its caller could not reach in the
	// interface. Whole packages rather than hand-picked routes, because a
	// list of paths here is a second copy of each package's route table,
	// and the set a command can actually reach is the set of commands
	// compiled into the binary, not the set of paths mounted on this mux.
	authHandlers.Routes(consoleAPI)
	apiKeyHandlers.Routes(consoleAPI)
	chatHandlers.Routes(consoleAPI)
	quotaHandlers.Routes(consoleAPI)
	usageHandlers.Routes(consoleAPI)
	cardHandlers.Routes(consoleAPI)
	inviteHandlers.Routes(consoleAPI)
	backupHandlers.Routes(consoleAPI)
	projectHandlers.Routes(consoleAPI)
	feedbackHandlers.Routes(consoleAPI)
	oauthHandlers.Routes(consoleAPI)

	// Read before the SSH server is built rather than from it: `help ssh`
	// prints the fingerprint, so the engine needs it, and the server needs
	// the engine. Loading the key is idempotent, so both read the same one.
	var sshInfo console.SSHInfo
	if cfg.Console.SSHAddr != "" {
		print, err := consolessh.HostKeyFingerprint(cfg.Console.SSHHostKey)
		if err != nil {
			return nil, err
		}
		sshInfo = console.SSHInfo{Enabled: true, Addr: cfg.Console.SSHAddr, Fingerprint: print}
	}

	consoleEngine := console.New(console.Options{
		Dispatch: console.NewDispatcher(consoleAPI),
		Version:  deps.Version,
		SiteName: func() string { return settingsService.Get(settings.SiteName) },
		SSH:      sshInfo,
		// The console can do everything the backoffice can from a surface
		// that leaves nothing on screen afterwards, so what was run is
		// recorded where the other administrative decisions are. The engine
		// has already masked every sensitive flag in Line.
		Audit: func(ctx context.Context, rec console.AuditRecord) {
			severity := securityevents.SeverityInfo
			if !rec.OK {
				severity = securityevents.SeverityWarning
			}
			if err := securityLog.Record(ctx, nil, securityevents.Event{
				Event: securityevents.EventConsoleCommand, Severity: severity,
				UserID: rec.Actor.ID, Username: rec.Actor.Username,
				ActorID: rec.Actor.ID, ActorUsername: rec.Actor.Username,
				IP: rec.IP, Source: rec.Transport, Decision: rec.Code, Reason: rec.Line,
			}); err != nil {
				slog.ErrorContext(ctx, "could not record a console command",
					"error", err, "actor", rec.Actor.Username)
			}
		},
	})
	// The work surface's tools are the console's own commands, filtered to
	// what the account may run. Set here rather than at construction
	// because the engine is built from the mux the handlers above are
	// mounted on, and the chat service is older than both.
	chatService.Tools = agent.New(consoleEngine)

	consoleHandlers := console.NewHandlers(consoleEngine)
	consoleHandlers.ClientIP = func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) }
	consoleHandlers.Allowed = func(ctx context.Context, account user.User) error {
		return terminalAllowed(ctx, groups, account)
	}
	consoleHandlers.Routes(mux)

	// The same engine, reachable without a browser. Off unless an address was
	// configured: a listener that accepts passwords should appear because an
	// operator asked for it, not because the software was upgraded.
	var sshServer *consolessh.Server
	if cfg.Console.SSHAddr != "" {
		sshServer, err = consolessh.New(consolessh.Config{
			Addr:        cfg.Console.SSHAddr,
			HostKeyPath: cfg.Console.SSHHostKey,
			Console:     consoleEngine,
			// The console's own door, held to the web login's rules: the same
			// Argon2id verification and the same attempt budget, so guessing
			// here and guessing at the sign-in form cannot be spread across
			// two limits.
			Authenticate: authService.VerifyCredential,
			// The web terminal's rule, so the two doors agree about who may
			// have a console: any account whose group allows it, and every
			// administrator.
			//
			// An account the two-step policy is holding at the door is held
			// here too: enrolling needs a screen to scan from, and SSH is
			// not one. Asked before every command, so a policy switched on
			// reaches a session that is already open.
			Permitted: func(ctx context.Context, account user.User) bool {
				return terminalAllowed(ctx, groups, account) == nil &&
					!authService.MustEnrolTwoFactor(account)
			},
			// The code for an account with two-step sign-in, checked against
			// the same secret, the same replay guard and the same guessing
			// budget as the web sign-in's second step.
			SecondFactor: authService.VerifyTwoFactorCode,
			// Each connection holds its own visit to the backoffice, opened
			// by `2fa backoffice <code>` and gone when it hangs up: the code
			// typed to sign in proves who connected, not that they are still
			// there when they reach for the administrative commands.
			ConnectionContext: auth.WithBackofficeGrant,
			// What the cookie does for the browser: the account is read
			// again before every command, so revoking a grant, disabling an
			// account or deleting it reaches a session that is already open
			// rather than waiting for whoever holds it to disconnect.
			Reauthorize: func(ctx context.Context, userID string) (user.User, error) {
				return users.ByID(ctx, nil, userID)
			},
			IdleTimeout: cfg.Console.SSHIdle,
			MaxSessions: cfg.Console.SSHMaxSessions,
		})
		if err != nil {
			return nil, err
		}
	}

	// Anything under /api that no module claimed is a client bug, and should
	// read as one instead of quietly returning the SPA shell.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NotFound("No such endpoint."))
	})

	// The PWA install card and splash screen. Public, like the shell itself:
	// a browser asks for it before anyone has signed in, and nothing in it
	// is more sensitive than the site name /api/site already discloses.
	mux.Handle("GET /manifest.webmanifest", web.ManifestHandler(func() web.Manifest {
		name := settingsService.Get(settings.PWAName)
		if name == "" {
			name = settingsService.Get(settings.SiteName)
		}
		shortName := settingsService.Get(settings.PWAShortName)
		if shortName == "" {
			shortName = name
		}
		description := settingsService.Get(settings.PWADescription)
		if description == "" {
			description = settingsService.Get(settings.SiteDescription)
		}
		iconURL := settingsService.Get(settings.PWAIconURL)
		if iconURL == "" && settingsService.SiteLogoUpdatedAt() > 0 {
			iconURL = fmt.Sprintf("/api/site/logo?v=%d", settingsService.SiteLogoUpdatedAt())
		}
		return web.Manifest{
			Name:            name,
			ShortName:       shortName,
			Description:     description,
			ThemeColor:      settingsService.Get(settings.PWAThemeColor),
			BackgroundColor: settingsService.Get(settings.PWABackgroundColor),
			IconURL:         iconURL,
		}
	}))

	frontend, err := web.Handler(web.Options{
		Dev:        cfg.Dev,
		DevServer:  devServerURL(cfg),
		Title:      settingsService.BrowserTitle,
		ThemeColor: func() string { return settingsService.Get(settings.PWAThemeColor) },
		FaviconURL: func() string {
			if settingsService.SiteLogoUpdatedAt() > 0 {
				return fmt.Sprintf("/api/site/logo?v=%d", settingsService.SiteLogoUpdatedAt())
			}
			return ""
		},
	})
	if err != nil {
		return nil, err
	}
	mux.Handle("/", frontend)

	handler := httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Recover(),
		httpx.Logger(),
		// Outside the session lookup so the duration it measures is the whole
		// answer and a request refused before any handler is still recorded;
		// inside the request id so both records name the same request.
		requestLog.Middleware(
			func(r *http.Request) string { return httpx.ClientIP(r, proxyTrust) },
			skipFromLog,
		),
		// Inside the log, so the byte count it records is what actually went
		// on the wire rather than what the handler produced. Outside
		// everything that writes a body, so there is one place that decides.
		httpx.Compress(),
		httpx.SecurityHeaders(cfg.Dev, web.InlineScriptHashes(), func() bool {
			// Exactly when a widget can appear. A key with both switches off
			// draws nothing, and an instance that draws nothing keeps the
			// policy it had before this feature existed.
			return settingsService.Get(settings.TurnstileSiteKey) != "" &&
				(settingsService.Bool(settings.TurnstileOnSignup) ||
					settingsService.Bool(settings.TurnstileOnLogin) ||
					settingsService.Bool(settings.TurnstileOnAPIKey) ||
					settingsService.Bool(settings.TurnstileOnRedeem) ||
					settingsService.Bool(settings.TurnstileOnFeedback) ||
					settingsService.Int(settings.ChatChallengeRequests, 0) > 0)
		}),
		httpx.SameOrigin(cfg.AllowedOrigins),
		// Last, so the session lookup only happens for requests that survived
		// the origin check.
		authService.Attach(),
		// Right after it: an account the two-step policy says must enrol
		// reaches nothing but the enrolment endpoints, whichever handler it
		// was asking for.
		authService.EnrolmentGate(),
		// After it, because the account it names only exists in the context
		// Attach created — which the log's own layer, further out, never sees.
		reqlog.Identify(func(r *http.Request) (string, string) {
			account, ok := auth.UserFrom(r.Context())
			if !ok {
				return "", ""
			}
			return account.ID, account.Username
		}),
	)

	return &Server{
		deps:          deps,
		handler:       handler,
		ssh:           sshServer,
		settings:      settingsService,
		auth:          authService,
		users:         users,
		conversations: conversations,
		quota:         quotaService,
		requests:      requestLog,
		idp:           idpStore,
		notify:        notifyStore,
		invites:       invites,
		consoleAPI:    consoleAPI,
		health: &health.Checker{
			Store: healthStore, Models: models, Providers: providers, Registry: registry, Notify: notifyStore,
		},
	}, nil
}

// apiAllowed reports whether this account may use the API: the instance-wide
// switch, an account restriction, then the grant on their group.
// Administrators bypass the latter two the way they bypass every other group
// restriction, but not the first — a disabled API is disabled for everyone.
//
// It backs the interface's "you cannot create a key" state. The /v1 surface
// checks the same conditions itself rather than calling this, because a
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
	if account.APIRestrictedAt(time.Now()) {
		return httpx.ForbiddenCode("api_restricted",
			"API access is restricted for this account.").
			WithDetails(map[string]any{"until": account.APIRestrictedUntil})
	}
	membership, err := groups.ByID(ctx, nil, account.GroupID)
	if err != nil || !membership.APIAccess {
		return httpx.ForbiddenCode("api_not_permitted",
			"Your group does not have API access.")
	}
	return nil
}

// deleteAllowed answers whether this account's group lets it remove its own
// conversations. An administrator always may: the capability exists to hold
// an instance's members to a record, not to lock its operator out of one.
//
// A group that cannot be read grants it, matching what the account payload
// tells the client, so a failed lookup does not silently take away something
// somebody has.
func deleteAllowed(ctx context.Context, groups *group.Store, account user.User) error {
	if account.IsAdmin() {
		return nil
	}
	membership, err := groups.ByID(ctx, nil, account.GroupID)
	if err != nil || membership.AllowDeleteConversations {
		return nil
	}
	return httpx.ForbiddenCode("delete_not_permitted",
		"Your group cannot delete conversations.")
}

// terminalAllowed answers whether this account's group lets it open the
// terminal. An administrator always may, whatever their group says: the
// terminal is where some of the backoffice's own work is done.
//
// Unlike deleteAllowed, a group that cannot be read refuses. The terminal is
// a way in rather than a thing somebody owns, and a lookup that failed is not
// a reason to open it.
func terminalAllowed(ctx context.Context, groups *group.Store, account user.User) error {
	if account.IsAdmin() {
		return nil
	}
	membership, err := groups.ByID(ctx, nil, account.GroupID)
	if err == nil && membership.AllowTerminal {
		return nil
	}
	return httpx.ForbiddenCode("terminal_not_permitted", "Your group cannot use the terminal.")
}

// archiveAllowed answers whether the instance's global settings permit archiving
// conversations. An administrator always may.
func archiveAllowed(_ context.Context, set *settings.Service, account user.User) error {
	if account.IsAdmin() {
		return nil
	}
	if set.Bool(settings.AllowArchive) {
		return nil
	}
	return httpx.ForbiddenCode("archive_not_permitted",
		"Archiving conversations is disabled.")
}

// skipFromLog drops the requests nobody audits: the compiled frontend's own
// assets. Everything else is recorded, including the ones that never reached
// a handler.
func skipFromLog(r *http.Request) bool {
	path := r.URL.Path
	return strings.HasPrefix(path, "/assets/") ||
		path == "/favicon.ico" ||
		path == "/robots.txt" ||
		path == "/manifest.webmanifest"
}

func (s *Server) Handler() http.Handler { return s.handler }

// SSH is the console's SSH listener, or nil when none was configured. main
// starts and stops it alongside the HTTP server; nothing else touches it.
func (s *Server) SSH() *consolessh.Server { return s.ssh }

// StartJanitor runs the one piece of periodic work this server has: expiring
// sessions. It is a single goroutine on a ticker, not a scheduler, and it
// stops when the context does.
// StartJanitor also starts the request log's writer, which is a goroutine
// with the same lifetime: it drains the queue while the context lives and
// writes whatever is left when it ends.
func (s *Server) StartJanitor(ctx context.Context) {
	go s.requests.Run(ctx)

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
	if err := s.users.ExpireMemberships(sweepCtx, nil, time.Now()); err != nil {
		slog.ErrorContext(sweepCtx, "could not expire group memberships", "error", err)
	}
	s.sweepAttachments(sweepCtx)
	// Authorisation codes live two minutes and tokens an hour; without this
	// the two tables grow forever with rows nothing will read again.
	if s.idp != nil {
		if _, err := s.idp.Purge(sweepCtx); err != nil {
			slog.ErrorContext(sweepCtx, "could not purge sign-in grants", "error", err)
		}
	}
	// Counter buckets whose window has long since rolled over. The ledger is
	// never pruned: it is the audit trail.
	_, _ = s.quota.PruneCounters(sweepCtx)
	// The bell keeps about a month, unlike the security log: a notice is a
	// nudge to look at something, not a record kept for its own sake.
	if s.notify != nil {
		if _, err := s.notify.Prune(sweepCtx, time.Now().Add(-30*24*time.Hour)); err != nil {
			slog.ErrorContext(sweepCtx, "could not prune notifications", "error", err)
		}
	}
	// Invite rewards waiting on something no request announces — a signup
	// restriction running out, say.
	if s.invites != nil {
		if err := s.invites.RewardPending(sweepCtx, s.auth.VerificationRequired()); err != nil {
			slog.ErrorContext(sweepCtx, "could not resolve pending invite rewards", "error", err)
		}
	}
	s.sweepHealth(ctx)
}

// sweepHealth asks the models nobody has used lately whether they still work,
// and acts on the answer.
//
// Its own context, not the 30-second one above: a pass talks to every
// provider an instance has, and one slow upstream must not cut the pass short
// for the models after it. The policy is read here rather than captured at
// boot, so a change in the settings screen lands on the next pass.
func (s *Server) sweepHealth(ctx context.Context) {
	if s.health == nil {
		return
	}
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	s.health.Run(healthCtx, health.Policy{
		Probe:        s.settings.Bool(settings.HealthProbe),
		Window:       time.Duration(s.settings.Int(settings.HealthWindowMins, 30)) * time.Minute,
		DisableAfter: s.settings.Int(settings.HealthDisableAfter, 0),
		Retain:       time.Duration(s.settings.Int(settings.HealthRetainDays, 14)) * 24 * time.Hour,
	})
}

// sweepAttachments applies the operator's retention policy: the orphan
// window, an optional age limit, and an optional daily purge.
//
// The policy is read here rather than captured at boot, so changing it takes
// effect on the next tick instead of on the next restart.
func (s *Server) sweepAttachments(ctx context.Context) {
	policy := conversation.Retention{
		AfterDays: s.settings.Int(settings.AttachmentPurgeDays, 0),
		DailyAt:   s.settings.Get(settings.AttachmentPurgeDaily),
		OrphanTTL: time.Duration(s.settings.Int(settings.AttachmentOrphanMins, 60)) * time.Minute,
	}
	lastRun := int64(s.settings.Int(settings.AttachmentPurgeLast, 0))

	// Configured but never run: record the time and purge nothing this pass.
	// An operator who sets a 03:00 cleanup at three in the afternoon did not
	// ask for everything to vanish right then.
	if conversation.DecidePurge(policy.DailyAt, time.Now(), lastRun) == conversation.DecisionSeed {
		if err := s.settings.Set(ctx, settings.AttachmentPurgeLast,
			strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
			slog.ErrorContext(ctx, "could not record the attachment purge time", "error", err)
		}
		policy.DailyAt = ""
	}

	result, err := s.conversations.Sweep(ctx, policy, lastRun)
	if err != nil {
		slog.ErrorContext(ctx, "attachment sweep failed", "error", err)
		return
	}
	if result.RanDaily {
		if err := s.settings.Set(ctx, settings.AttachmentPurgeLast,
			strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
			slog.ErrorContext(ctx, "could not record the attachment purge time", "error", err)
		}
	}
	// Worth a line in the log: this is the one background task that destroys
	// something, and an operator should be able to see that it is working.
	if result.Aged > 0 || result.Purged > 0 || result.Orphans > 0 {
		slog.InfoContext(ctx, "swept attachments",
			"aged_out", result.Aged, "purged", result.Purged,
			"orphans_removed", result.Orphans, "daily_purge", result.RanDaily)
	}
}

func devServerURL(cfg config.Config) string {
	if !cfg.Dev {
		return ""
	}
	return "http://127.0.0.1:5173"
}
