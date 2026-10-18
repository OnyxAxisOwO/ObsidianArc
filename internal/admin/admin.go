// Package admin is the administrative surface: everything an operator can do
// that a regular user cannot.
//
// It is a transport layer over the other modules rather than a module with
// its own tables. Keeping it separate is what makes "which endpoints require
// an administrator" answerable by listing one package's routes, and it keeps
// the privileged operations from sitting next to the unprivileged ones in
// each module's own handlers, where a missing middleware would be easy to
// miss.
package admin

import (
	"context"
	"errors"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/checkin"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/announcement"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/audit"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/feedback"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/idp"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/systembackup"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/update"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usercheck"
)

type Handlers struct {
	db            *database.DB
	users         *user.Store
	groups        *group.Store
	providers     *provider.Store
	models        *model.Store
	settings      *settings.Service
	registry      *adapter.Registry
	auth          *auth.Service
	usage         *usage.Store
	quota         *quota.Service
	conversations *conversation.Store
	announcements *announcement.Store
	keys          *apikey.Store
	requests      *reqlog.Store
	security      *securityevents.Store
	cards         *card.Store
	health        *health.Store
	feedback      *feedback.Store
	apps          *idp.Store
	invites       *invite.Store
	// Mail owns the database override and the live sender. Set by server
	// wiring so environment fallbacks and administrator saves share one path.
	Mail *mail.Manager
	// UserCheck is an optional paid disposable-address check, configured only
	// by an administrator and never through environment settings.
	UserCheck *usercheck.Manager
	// Runs the sign-up reviewer on a hypothetical account. Set by the wiring;
	// nil where no reviewer exists.
	TryReview func(ctx context.Context, in ReviewTrial) (string, string, error)
	// The address this instance answers at. Set by the wiring, which resolves
	// it the same way every other outward-facing URL is resolved; the
	// applications screen shows it because it is what an operator has to
	// paste into the software on the other side.
	Origin func(*http.Request) string
	// The caller's address as the proxy settings resolve it, for the
	// security log. Nil records none.
	ClientIP func(*http.Request) string
	// Where an administrative action tells the account it acted on, or every
	// administrator, that something happened. Set by the wiring; nil means
	// "push nothing", which is every instance predating this feature.
	Notify *notify.Store
	// The bonus bars; nil on a server built without them, which answers 404.
	Bonus *bonus.Store
	// The administrators' audit trail. Set by the wiring; nil records
	// nothing and leaves the trail's own routes unmounted.
	Audit *audit.Store
	// The check-in rewards, edited from the backoffice.
	Checkin *checkin.Service
	// SystemBackup covers the complete instance and is restricted to the
	// single super administrator rather than delegated settings operators.
	SystemBackup *systembackup.Service
	// The release check behind GET /api/admin/update. Nil answers "unknown"
	// and nothing is asked of GitHub.
	Updates *update.Checker
	// Whether a trusted proxy has sent Cloudflare headers this instance was
	// not told to expect. Set by the wiring, which owns the proxy settings.
	CloudflareUnclaimed func() bool

	UsageLimiter *httpx.TokenBucketLimiter

	// Paid calls to upstream models, one budget per instance. Each sign-up
	// review and each model probe spends provider credit whatever its caller,
	// so the budget is not per caller. Refusing rather than queueing matches
	// the test-send cooldowns. It lives in memory, so a second instance keeps
	// its own budget, the same trade the usage limiter makes.
	reviewBudget *httpx.TokenBucketLimiter
	probeBudget  *httpx.TokenBucketLimiter

	usageCacheMu        sync.RWMutex
	usageSummaryCache   map[string]*cachedUsageSummary
	usageBreakdownCache map[string]*cachedUsageBreakdown
	usageSummarySF      singleflight.Group
	usageBreakdownSF    singleflight.Group
	// The allowances ranking is built once per search and group for each
	// short window, and paged from that copy; see allowances.go.
	allowanceSF singleflight.Group
	// Bumped by every write that changes what an account has left, so a ranking
	// built before the write is neither cached nor handed to a request after
	// it. Guarded by usageCacheMu; see forgetAllowanceRankings.
	allowanceGeneration uint64

	// The resources page's storage totals, read at most once per
	// storageCacheTTL; see resources.go.
	storageMu    sync.Mutex
	storageCache *storageTotals
	storageSF    singleflight.Group

	// Not injected: it is two fields of state that only the resources page
	// has any use for, and it is meaningless before the first request.
	cpu cpuSampler

	// Endpoints a plugin brought, mounted by Routes behind the same wrapper
	// as the table there.
	extra []Route
	// Set once Routes has read extra. A route mounted after that would be
	// accepted and then never served, with no error anywhere.
	routed bool
	// Which plugins are on. A switched-off plugin's routes answer 404, the
	// same as a build without it.
	gate plugingate.Gate
	// Where the routes of plugins installed while the server runs are
	// served from; see SetFallback.
	fallback atomic.Pointer[func(w http.ResponseWriter, r *http.Request) bool]
}

// SetPluginGate is how the server says which plugins are on. Routes reads it
// on each request rather than at mount time, so a gate set late still applies
// from then on. Until it is set, a nil gate lets every plugin route through
// (see plugingate), so the server sets it before it starts serving.
func (h *Handlers) SetPluginGate(g plugingate.Gate) { h.gate = g }

// PluginRoutes lists the endpoints plugin mounted, for its manifest.
func (h *Handlers) PluginRoutes(plugin string) []Route {
	var out []Route
	for _, route := range h.extra {
		if route.Plugin == plugin {
			out = append(out, route)
		}
	}
	return out
}

// Route is one administrative endpoint a plugin adds. Pattern is the full
// ServeMux pattern and must sit under /api/admin/; Permission is a grant
// list as the table in Routes spells it.
type Route struct {
	Pattern    string
	Permission string
	Handler    httpx.Handler
	// The plugin that brought it; empty for one the core mounts this way.
	Plugin string
}

// Mount adds a plugin's endpoint. It has to be called before Routes, which is
// the only thing that reads the list: the wiring sets plugins up first.
func (h *Handlers) Mount(route Route) {
	if !strings.Contains(route.Pattern, " /api/admin/") {
		panic("admin: a plugin route must live under /api/admin/: " + route.Pattern)
	}
	// Refused loudly: a route accepted here after Routes ran would be silently
	// absent from the table, and nothing would ever say so.
	if h.routed {
		panic("admin: a plugin route mounted after Routes would never be served: " + route.Pattern)
	}
	h.extra = append(h.extra, route)
}

func NewHandlers(
	db *database.DB,
	users *user.Store,
	groups *group.Store,
	providers *provider.Store,
	models *model.Store,
	set *settings.Service,
	registry *adapter.Registry,
	authService *auth.Service,
	usageStore *usage.Store,
	quotaService *quota.Service,
	conversations *conversation.Store,
	announcements *announcement.Store,
	keys *apikey.Store,
	requests *reqlog.Store,
	securityLog *securityevents.Store,
	cards *card.Store,
	healthStore *health.Store,
	feedbackStore *feedback.Store,
	apps *idp.Store,
	invites *invite.Store,
) *Handlers {
	return &Handlers{
		db:                  db,
		users:               users,
		groups:              groups,
		providers:           providers,
		models:              models,
		settings:            set,
		registry:            registry,
		auth:                authService,
		usage:               usageStore,
		quota:               quotaService,
		conversations:       conversations,
		announcements:       announcements,
		keys:                keys,
		requests:            requests,
		security:            securityLog,
		cards:               cards,
		health:              healthStore,
		feedback:            feedbackStore,
		apps:                apps,
		invites:             invites,
		UsageLimiter:        httpx.NewTokenBucketLimiter(5, 10),
		reviewBudget:        httpx.NewTokenBucketLimiter(1.0/10, 3),
		probeBudget:         httpx.NewTokenBucketLimiter(1.0/30, 1),
		usageSummaryCache:   make(map[string]*cachedUsageSummary),
		usageBreakdownCache: make(map[string]*cachedUsageBreakdown),
	}
}

// Protect wraps handler in the backoffice's checks: a signed-in administrator,
// the two-step policy, and permission — a grant list as the table in Routes
// spells it, or "super_admin". Every write it is asked for, done or refused,
// goes into the audit trail on the way. Routes applies it to every endpoint
// it mounts, and it is exported for the plugins that are installed while the
// server runs, whose routes cannot be in that table.
func (h *Handlers) Protect(permission string, handler httpx.Handler) http.Handler {
	return auth.RequireAdmin(h.audited(httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		actor := auth.MustUser(r.Context())
		if h.auth.BackofficeNeedsTwoFactor(actor) {
			return backofficeNeedsTwoFactor()
		}
		if h.auth.BackofficeLocked(r.Context(), actor) {
			return backofficeLocked()
		}
		if permission == "super_admin" {
			if !actor.IsSuperAdmin() {
				return permissionDenied()
			}
		} else if !hasPermission(actor, permission) {
			return permissionDenied()
		}
		h.auth.KeepBackofficeOpen(r.Context(), actor)
		return handler(w, r)
	})))
}

// SetFallback installs the answer to "does a plugin installed while the
// server runs serve this?", asked for every /api/admin/ request no route in
// the table claimed. It reports whether it did; if not the request is a 404
// like any other.
func (h *Handlers) SetFallback(fn func(w http.ResponseWriter, r *http.Request) bool) {
	h.fallback.Store(&fn)
}

// Routes mounts every administrative endpoint behind RequireAdmin. One
// wrapper, applied here, rather than a check inside each handler: a new
// endpoint added to this list is protected by being on the list.
//
// The two-step policy is held here too, for the same reason and one more:
// the console, over the web and over SSH, dispatches into these same routes,
// so this is the one place the backoffice's second lock cannot be walked
// around.
func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := h.Protect
	h.routed = true

	mux.Handle("GET /api/admin/dashboard", protected("dashboard", h.dashboard))
	mux.Handle("GET /api/admin/resources", protected("resources", h.resources))
	mux.Handle("GET /api/admin/health", protected("availability", h.modelHealth))
	mux.Handle("POST /api/admin/health/probe", protected("availability", h.probeAllHealth))
	mux.Handle("POST /api/admin/health/reset", protected("availability", h.resetHealth))
	mux.Handle("POST /api/admin/security/review", protected("security", h.trialReview))
	mux.Handle("GET /api/admin/security/events", protected("security", h.listSecurityEvents))
	mux.Handle("GET /api/admin/security/two-factor", protected("security", h.twoFactorAdoption))
	mux.Handle("GET /api/admin/mail", protected("security", h.getMail))
	mux.Handle("PUT /api/admin/mail", protected("security", h.putMail))
	mux.Handle("POST /api/admin/mail/test", protected("security", h.testMail))
	mux.Handle("GET /api/admin/usercheck", protected("security", h.getUserCheck))
	mux.Handle("PUT /api/admin/usercheck", protected("security", h.putUserCheck))
	mux.Handle("POST /api/admin/usercheck/test", protected("security", h.testUserCheck))
	// The applications that may use this instance as a sign-in. Under the
	// security grant rather than a page grant of their own: they are part
	// of the same subject as the front door, and they live on the same
	// screen as the providers this instance signs in *with*.
	mux.Handle("GET /api/admin/applications", protected("security", h.listApps))
	mux.Handle("POST /api/admin/applications", protected("security", h.createApp))
	mux.Handle("PATCH /api/admin/applications/{id}", protected("security", h.updateApp))
	mux.Handle("POST /api/admin/applications/{id}/secret", protected("security", h.rotateAppSecret))
	mux.Handle("DELETE /api/admin/applications/{id}", protected("security", h.deleteApp))

	mux.Handle("GET /api/admin/users", protected("users", h.listUsers))
	mux.Handle("GET /api/admin/users/{id}", protected("users", h.showUser))
	mux.Handle("PATCH /api/admin/users/{id}", protected("users", h.updateUser))
	mux.Handle("DELETE /api/admin/users/{id}", protected("users", h.deleteUser))
	mux.Handle("POST /api/admin/users", protected("users", h.createUser))
	mux.Handle("POST /api/admin/users/{id}/password", protected("users", h.resetPassword))
	mux.Handle("DELETE /api/admin/users/{id}/two-factor", protected("users", h.resetTwoFactor))
	mux.Handle("GET /api/admin/users/{id}/keys", protected("users", h.userKeys))
	mux.Handle("DELETE /api/admin/users/{id}/keys/{key}", protected("users", h.revokeUserKey))
	mux.Handle("GET /api/admin/users/{id}/sessions", protected("users", h.userSessions))
	mux.Handle("DELETE /api/admin/users/{id}/sessions/{sid}", protected("users", h.revokeUserSession))
	mux.Handle("DELETE /api/admin/users/{id}/sessions", protected("users", h.revokeAllUserSessions))
	mux.Handle("GET /api/admin/users/{id}/conversations", protected("users", h.userConversations))
	mux.Handle("GET /api/admin/users/{id}/conversations/{conversation}", protected("users", h.userTranscript))

	mux.Handle("GET /api/admin/groups", protected("groups", h.listGroups))
	mux.Handle("POST /api/admin/groups", protected("groups", h.createGroup))
	mux.Handle("PATCH /api/admin/groups/{id}", protected("groups", h.updateGroup))
	mux.Handle("POST /api/admin/groups/{id}/members", protected("groups", h.assignGroupMembers))
	mux.Handle("DELETE /api/admin/groups/{id}", protected("groups", h.deleteGroup))

	mux.Handle("GET /api/admin/settings", protected("settings,security,availability,invites,leaderboard", h.listSettings))
	mux.Handle("PUT /api/admin/settings", protected("settings,security,availability,invites,leaderboard", h.updateSettings))
	mux.Handle("POST /api/admin/settings/import", protected("settings", h.importSettings))
	mux.Handle("POST /api/admin/attachments/purge", protected("settings", h.purgeAttachments))
	mux.Handle("PUT /api/admin/login-background/{variant}", protected("settings", h.putLoginBackground))
	mux.Handle("DELETE /api/admin/login-background/{variant}", protected("settings", h.deleteLoginBackground))
	mux.Handle("PUT /api/admin/logo", protected("settings", h.putLogo))
	mux.Handle("DELETE /api/admin/logo", protected("settings", h.deleteLogo))

	mux.Handle("GET /api/admin/providers", protected("providers", h.listProviders))
	mux.Handle("POST /api/admin/providers", protected("providers", h.createProvider))
	mux.Handle("PATCH /api/admin/providers/{id}", protected("providers", h.updateProvider))
	mux.Handle("DELETE /api/admin/providers/{id}", protected("providers", h.deleteProvider))
	mux.Handle("POST /api/admin/providers/{id}/detect", protected("providers,models", h.detectModels))

	mux.Handle("GET /api/admin/models", protected("models", h.listModels))
	mux.Handle("POST /api/admin/models", protected("models", h.createModel))
	mux.Handle("PATCH /api/admin/models/{id}", protected("models", h.updateModel))
	mux.Handle("DELETE /api/admin/models/{id}", protected("models", h.deleteModel))
	mux.Handle("PUT /api/admin/models/order", protected("models", h.reorderModels))
	mux.Handle("POST /api/admin/models/import", protected("models", h.importModels))

	mux.Handle("GET /api/admin/logs", protected("logs", h.listLogs))
	mux.Handle("GET /api/admin/logs/facets", protected("logs", h.logFacets))
	mux.Handle("POST /api/admin/logs/prune", protected("logs", h.pruneLogs))

	mux.Handle("GET /api/admin/usage", protected("usage", h.usageSummary))
	mux.Handle("GET /api/admin/usage/rpm", protected("usage", h.currentRPM))
	mux.Handle("GET /api/admin/usage/breakdown", protected("usage", h.usageBreakdown))
	mux.Handle("GET /api/admin/usage/records", protected("usage", h.usageRecords))
	mux.Handle("GET /api/admin/usage/allowances", protected("usage", h.usageAllowances))
	mux.Handle("POST /api/admin/usage/reset", protected("usage", h.resetQuota))
	// Like the bonus routes below, mounted only with the service behind them: the
	// handlers call straight into it, so without one the request would panic
	// rather than answer 404.
	if h.Checkin != nil {
		mux.Handle("GET /api/admin/checkin", protected("usage", h.checkinSettings))
		mux.Handle("PUT /api/admin/checkin", protected("usage", h.saveCheckinSettings))
	}
	// Without a bonus store these routes are not mounted, so the mux answers
	// 404 as the Bonus field promises. The handlers would otherwise
	// dereference the nil store.
	if h.Bonus != nil {
		mux.Handle("GET /api/admin/bonus/bars", protected("usage", h.bonusBars))
		mux.Handle("POST /api/admin/bonus/bars", protected("usage", h.createBonusBar))
		mux.Handle("PUT /api/admin/bonus/bars/{id}", protected("usage", h.updateBonusBar))
		mux.Handle("DELETE /api/admin/bonus/bars/{id}", protected("usage", h.deleteBonusBar))
		mux.Handle("POST /api/admin/bonus/bars/{id}/grants", protected("usage", h.grantBonus))
		mux.Handle("GET /api/admin/bonus/bars/{id}/grants", protected("usage", h.bonusGrants))
		mux.Handle("DELETE /api/admin/bonus/grants/{id}", protected("usage", h.revokeBonusGrant))
		mux.Handle("POST /api/admin/bonus/bars/{id}/pool", protected("usage", h.adjustBonusPool))
	}
	mux.Handle("GET /api/admin/codes", protected("codes", h.listCodes))
	mux.Handle("POST /api/admin/codes", protected("codes", h.createCode))
	mux.Handle("GET /api/admin/codes/{id}/redemptions", protected("codes", h.codeRedemptions))
	mux.Handle("DELETE /api/admin/codes/{id}", protected("codes", h.deleteCode))
	mux.Handle("POST /api/admin/users/{id}/cards", protected("users", h.grantCards))
	mux.Handle("PATCH /api/admin/users/{id}/cards", protected("users", h.rescheduleCards))
	mux.Handle("DELETE /api/admin/users/{id}/cards/{card}", protected("users", h.revokeCard))

	mux.Handle("GET /api/admin/quota/policies", protected("users,groups,usage", h.listPolicies))
	mux.Handle("PUT /api/admin/quota/policies", protected("users,groups,usage", h.savePolicy))
	mux.Handle("DELETE /api/admin/quota/policies/{scope}", protected("users,groups,usage", h.deletePolicy))

	mux.Handle("GET /api/admin/announcements", protected("announcements", h.listAnnouncements))
	mux.Handle("POST /api/admin/announcements", protected("announcements", h.createAnnouncement))
	mux.Handle("PATCH /api/admin/announcements/{id}", protected("announcements", h.updateAnnouncement))
	mux.Handle("DELETE /api/admin/announcements/{id}", protected("announcements", h.deleteAnnouncement))

	mux.Handle("GET /api/admin/feedback", protected("feedback", h.listFeedback))
	mux.Handle("GET /api/admin/feedback/{id}", protected("feedback", h.showFeedback))
	mux.Handle("PATCH /api/admin/feedback/{id}", protected("feedback", h.updateFeedback))
	mux.Handle("DELETE /api/admin/feedback/{id}", protected("feedback", h.deleteFeedback))
	mux.Handle("POST /api/admin/feedback/{id}/replies", protected("feedback", h.replyToFeedback))
	mux.Handle("DELETE /api/admin/feedback/{id}/replies/{reply}", protected("feedback", h.deleteFeedbackReply))

	mux.Handle("GET /api/admin/invites", protected("invites", h.listInvites))
	mux.Handle("POST /api/admin/invites", protected("invites", h.createInvites))
	mux.Handle("DELETE /api/admin/invites/{id}", protected("invites", h.revokeInvite))
	mux.Handle("GET /api/admin/invites/{id}/uses", protected("invites", h.inviteUses))
	mux.Handle("GET /api/admin/invites/stats", protected("invites", h.inviteStats))

	mux.Handle("GET /api/admin/references", protected("", h.references))
	mux.Handle("GET /api/admin/member-options", protected("groups", h.listMemberOptions))
	mux.Handle("GET /api/admin/meta", protected("", h.meta))
	mux.Handle("GET /api/admin/backup", protected("super_admin", h.getSystemBackup))
	mux.Handle("PUT /api/admin/backup", protected("super_admin", h.saveSystemBackup))
	mux.Handle("POST /api/admin/backup/test", protected("super_admin", h.testSystemBackup))
	mux.Handle("POST /api/admin/backup/run", protected("super_admin", h.runSystemBackup))
	mux.Handle("GET /api/admin/update", protected("super_admin", h.updateStatus))
	// Read-only on purpose: nothing deletes from the trail but the retention.
	if h.Audit != nil {
		mux.Handle("GET /api/admin/audit", protected("audit", h.listAudit))
		mux.Handle("GET /api/admin/audit/facets", protected("audit", h.auditFacets))
		mux.Handle("GET /api/admin/audit/{id}", protected("audit", h.showAudit))
	}

	for _, route := range h.extra {
		inner := protected(route.Permission, route.Handler)
		owner := route.Plugin
		// Before the permission check rather than after it: a switched-off
		// plugin's route is not there, for anyone.
		mux.Handle(route.Pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !h.gate.Allows(owner) {
				httpx.WriteError(w, r, httpx.NotFound("Not found."))
				return
			}
			inner.ServeHTTP(w, r)
		}))
	}

	// Whatever else is under /api/admin/: a plugin installed since the server
	// started, or nothing at all.
	mux.HandleFunc("/api/admin/", func(w http.ResponseWriter, r *http.Request) {
		if fn := h.fallback.Load(); fn != nil && (*fn)(w, r) {
			return
		}
		httpx.WriteError(w, r, httpx.NotFound("No such endpoint."))
	})
}

// meta is the reference data the admin forms need: which provider kinds this
// build supports, which reasoning styles exist. Served rather than hard-coded
// in the frontend so a new adapter shows up in the UI without a rebuild of
// the client.
func (h *Handlers) meta(w http.ResponseWriter, _ *http.Request) error {
	kinds := make([]string, 0, 2)
	for _, kind := range h.registry.Kinds() {
		kinds = append(kinds, string(kind))
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"provider_kinds": kinds,
		"reasoning_styles": []string{
			string(adapter.ReasoningAuto),
			string(adapter.ReasoningNone),
			string(adapter.ReasoningAnthropic),
			string(adapter.ReasoningEffort),
			string(adapter.ReasoningOpenRouter),
			string(adapter.ReasoningQwen),
		},
	})
}

// pathID reads and validates an identifier from the route pattern before it
// reaches a query.
func pathID(r *http.Request, name string) (string, error) {
	value := r.PathValue(name)
	if !id.Valid(value) {
		return "", httpx.BadRequest("Malformed identifier.")
	}
	return value, nil
}

func translateProviderError(err error) error {
	switch {
	case errors.Is(err, provider.ErrNotFound):
		return httpx.NotFound("No such provider.")
	case errors.Is(err, provider.ErrNameTaken):
		return httpx.Conflict("provider_exists", "A provider with that name already exists.")
	case errors.Is(err, provider.ErrInvalidName):
		return httpx.BadRequest("Name must be 1-60 characters.")
	case errors.Is(err, provider.ErrInvalidKind):
		return httpx.BadRequest("Provider type must be openai or anthropic.")
	case errors.Is(err, provider.ErrInvalidStyle):
		return httpx.BadRequest("Unknown reasoning style.")
	case errors.Is(err, provider.ErrRouted):
		return httpx.Conflict("provider_routed", "Models on other providers route into this one. Clear those routes first.")
	case errors.Is(err, provider.ErrKeyRequired):
		return httpx.BadRequest("An API key is required.")
	case errors.Is(err, provider.ErrKeyNeededForMove):
		return httpx.BadRequestCode("provider_key_needed", "A new base URL needs the API key entered again.")
	case errors.Is(err, provider.ErrBaseURLNeedsSuperAdmin):
		return httpx.ForbiddenCode("super_admin_required", "Only a super administrator can set or change a provider's base URL.")
	case errors.Is(err, provider.ErrTooManyHeaders):
		return httpx.BadRequest("At most 20 extra headers.")
	case errors.Is(err, provider.ErrHiddenHeader):
		return httpx.BadRequestCode("header_hidden", "A header value shown as a hint cannot be saved. Enter the value again.")
	case errors.Is(err, provider.ErrInvalidHeaderName):
		// sanitizeHeaders wraps the sentinel with the refused name; the person
		// needs it to find the row, since the same save fails until it is fixed.
		// The name also travels as a detail so the page can word the refusal in
		// its own language with the name in place.
		name := headerNameFrom(err, provider.ErrInvalidHeaderName)
		return httpx.BadRequestCode("header_name_invalid", "Header name %s is not valid. A header name may use only letters, digits and !#$%%&'*+-.^_`|~.", name).
			WithDetails(map[string]any{"name": name})
	case errors.Is(err, provider.ErrDuplicateHeader):
		// Named for the same reason as the invalid name: two rows collide, and
		// the person has to know which one to look for.
		name := headerNameFrom(err, provider.ErrDuplicateHeader)
		return httpx.BadRequestCode("header_duplicate", "Header name %s matches another header once spaces are trimmed and letter case is ignored. Keep one of them.", name).
			WithDetails(map[string]any{"name": name})
	case errors.Is(err, provider.ErrTooManyKeys):
		return httpx.BadRequest("At most %d API keys.", provider.MaxAPIKeys)
	case errors.Is(err, provider.ErrKeyTooLong):
		return httpx.BadRequest("An API key is at most %d characters.", provider.MaxAPIKeyChars)
	case errors.Is(err, provider.ErrInvalidRotation):
		return httpx.BadRequest("Key rotation must be sequential or random.")
	case errors.Is(err, provider.ErrKeysChanged):
		return httpx.Conflict("provider_keys_changed", "The API keys were changed meanwhile. Open the provider again.")
	case errors.Is(err, provider.ErrInvalidBaseURL):
		return httpx.BadRequest("%s", strings.TrimPrefix(err.Error(), "provider: "))
	default:
		// A base-URL rejection is a validation message written for a person,
		// so it is passed through rather than swallowed into a 500.
		var adapterErr *adapter.Error
		if errors.As(err, &adapterErr) {
			return httpx.BadRequest("%s", adapterErr.Message)
		}
		return httpx.Internal(err)
	}
}

// headerNameFrom returns the name sanitizeHeaders appended to a header refusal,
// or "" when there is none. It matches the sentinel's own text instead of
// trimming blindly, so a refusal that carries no name cannot have its whole
// sentence shown as one.
func headerNameFrom(err error, sentinel error) string {
	name, ok := strings.CutPrefix(err.Error(), sentinel.Error()+": ")
	if !ok {
		return ""
	}
	return name
}
