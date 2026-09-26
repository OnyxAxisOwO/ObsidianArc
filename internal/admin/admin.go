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
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/announcement"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
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
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
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

	// Not injected: it is two fields of state that only the resources page
	// has any use for, and it is meaningless before the first request.
	cpu cpuSampler
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
		db:            db,
		users:         users,
		groups:        groups,
		providers:     providers,
		models:        models,
		settings:      set,
		registry:      registry,
		auth:          authService,
		usage:         usageStore,
		quota:         quotaService,
		conversations: conversations,
		announcements: announcements,
		keys:          keys,
		requests:      requests,
		security:      securityLog,
		cards:         cards,
		health:        healthStore,
		feedback:      feedbackStore,
		apps:          apps,
		invites:       invites,
	}
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
	protected := func(permission string, handler httpx.Handler) http.Handler {
		return auth.RequireAdmin(httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
			actor := auth.MustUser(r.Context())
			if h.auth.BackofficeNeedsTwoFactor(actor) {
				return backofficeNeedsTwoFactor()
			}
			if h.auth.BackofficeLocked(r.Context(), actor) {
				return backofficeLocked()
			}
			if !hasPermission(actor, permission) {
				return permissionDenied()
			}
			h.auth.KeepBackofficeOpen(r.Context(), actor)
			return handler(w, r)
		}))
	}

	mux.Handle("GET /api/admin/dashboard", protected("dashboard", h.dashboard))
	mux.Handle("GET /api/admin/resources", protected("resources", h.resources))
	mux.Handle("GET /api/admin/health", protected("availability", h.modelHealth))
	mux.Handle("POST /api/admin/health/probe", protected("availability", h.probeAllHealth))
	mux.Handle("POST /api/admin/health/reset", protected("availability", h.resetHealth))
	mux.Handle("POST /api/admin/security/review", protected("security", h.trialReview))
	mux.Handle("GET /api/admin/security/events", protected("security", h.listSecurityEvents))
	mux.Handle("GET /api/admin/security/two-factor", protected("security", h.twoFactorAdoption))
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
	mux.Handle("POST /api/admin/usage/reset", protected("usage", h.resetQuota))
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
	case errors.Is(err, provider.ErrKeyRequired):
		return httpx.BadRequest("An API key is required.")
	case errors.Is(err, provider.ErrTooManyHeaders):
		return httpx.BadRequest("At most 20 extra headers.")
	default:
		// A base-URL rejection is a validation message written for a person,
		// so it is passed through rather than swallowed into a 500.
		var adapterErr *adapter.Error
		if errors.As(err, &adapterErr) {
			return httpx.BadRequest("%s", adapterErr.Message)
		}
		if isValidationMessage(err) {
			return httpx.BadRequest("%s", strings.TrimPrefix(err.Error(), "provider: "))
		}
		return httpx.Internal(err)
	}
}

// The provider store's validation failures are sentences written for a
// person ("base URL must use https…"), wrapped rather than enumerated as
// sentinels. Recognising them keeps a typo in a form from being reported as
// a server fault.
func isValidationMessage(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"base url", "credentials", "https"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// asAdapter is errors.As with the adapter error type, named so the call sites
// above read as a question rather than as plumbing.
func asAdapter(err error, target **adapter.Error) bool {
	return errors.As(err, target)
}
