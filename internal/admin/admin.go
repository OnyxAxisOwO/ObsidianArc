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
	"errors"
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/announcement"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type Handlers struct {
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
}

func NewHandlers(
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
) *Handlers {
	return &Handlers{
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
	}
}

// Routes mounts every administrative endpoint behind RequireAdmin. One
// wrapper, applied here, rather than a check inside each handler: a new
// endpoint added to this list is protected by being on the list.
func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireAdmin(httpx.Wrap(handler))
	}

	mux.Handle("GET /api/admin/dashboard", protected(h.dashboard))

	mux.Handle("GET /api/admin/users", protected(h.listUsers))
	mux.Handle("GET /api/admin/users/{id}", protected(h.showUser))
	mux.Handle("PATCH /api/admin/users/{id}", protected(h.updateUser))
	mux.Handle("DELETE /api/admin/users/{id}", protected(h.deleteUser))
	mux.Handle("POST /api/admin/users/{id}/password", protected(h.resetPassword))
	mux.Handle("GET /api/admin/users/{id}/keys", protected(h.userKeys))
	mux.Handle("DELETE /api/admin/users/{id}/keys/{key}", protected(h.revokeUserKey))
	mux.Handle("GET /api/admin/users/{id}/conversations", protected(h.userConversations))
	mux.Handle("GET /api/admin/users/{id}/conversations/{conversation}", protected(h.userTranscript))

	mux.Handle("GET /api/admin/groups", protected(h.listGroups))
	mux.Handle("POST /api/admin/groups", protected(h.createGroup))
	mux.Handle("PATCH /api/admin/groups/{id}", protected(h.updateGroup))
	mux.Handle("DELETE /api/admin/groups/{id}", protected(h.deleteGroup))

	mux.Handle("GET /api/admin/settings", protected(h.listSettings))
	mux.Handle("PUT /api/admin/settings", protected(h.updateSettings))
	mux.Handle("POST /api/admin/settings/import", protected(h.importSettings))
	mux.Handle("POST /api/admin/attachments/purge", protected(h.purgeAttachments))

	mux.Handle("GET /api/admin/providers", protected(h.listProviders))
	mux.Handle("POST /api/admin/providers", protected(h.createProvider))
	mux.Handle("PATCH /api/admin/providers/{id}", protected(h.updateProvider))
	mux.Handle("DELETE /api/admin/providers/{id}", protected(h.deleteProvider))
	mux.Handle("POST /api/admin/providers/{id}/detect", protected(h.detectModels))

	mux.Handle("GET /api/admin/models", protected(h.listModels))
	mux.Handle("POST /api/admin/models", protected(h.createModel))
	mux.Handle("PATCH /api/admin/models/{id}", protected(h.updateModel))
	mux.Handle("DELETE /api/admin/models/{id}", protected(h.deleteModel))

	mux.Handle("GET /api/admin/usage", protected(h.usageSummary))
	mux.Handle("GET /api/admin/usage/records", protected(h.usageRecords))

	mux.Handle("GET /api/admin/quota/policies", protected(h.listPolicies))
	mux.Handle("PUT /api/admin/quota/policies", protected(h.savePolicy))
	mux.Handle("DELETE /api/admin/quota/policies/{scope}", protected(h.deletePolicy))

	mux.Handle("GET /api/admin/announcements", protected(h.listAnnouncements))
	mux.Handle("POST /api/admin/announcements", protected(h.createAnnouncement))
	mux.Handle("PATCH /api/admin/announcements/{id}", protected(h.updateAnnouncement))
	mux.Handle("DELETE /api/admin/announcements/{id}", protected(h.deleteAnnouncement))

	mux.Handle("GET /api/admin/meta", protected(h.meta))
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
