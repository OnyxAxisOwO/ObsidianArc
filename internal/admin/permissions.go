package admin

import (
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func permissionDenied() error {
	return httpx.ForbiddenCode("admin_permission_denied", "You do not have permission to access this page or perform this action.")
}

func hasPermission(account user.User, permissions string) bool {
	if permissions == "" {
		return account.IsAdmin()
	}
	for _, permission := range strings.Split(permissions, ",") {
		if account.CanAdmin(permission) {
			return true
		}
	}
	return false
}

// settingPermission names the grants, comma-separated as hasPermission reads
// them, that may read and write one setting. The shared settings route lets
// in any of them; which keys each one then sees and saves is decided here,
// key by key, so a grant reaches its own page's settings and nobody else's.
func settingPermission(key string) string {
	switch {
	case strings.HasPrefix(key, "health."):
		return "availability"
	case strings.HasPrefix(key, "leaderboard."):
		return "leaderboard"
	// The invites page's registration-mode select writes this switch as well
	// as invites.required: "invite only" is registration on, with a code
	// required. An operator trusted with invites and not with the rest of
	// security can open and close sign-ups, and nothing else there.
	case key == "registration.enabled":
		return "security,invites"
	case strings.HasPrefix(key, "registration."), strings.HasPrefix(key, "turnstile."),
		strings.HasPrefix(key, "security."), strings.HasPrefix(key, "oauth."):
		return "security"
	case strings.HasPrefix(key, "invites."):
		return "invites"
	default:
		return "settings"
	}
}

func canPolicy(actor user.User, scope quota.Scope) bool {
	if actor.CanAdmin("usage") {
		return true
	}
	return (scope == quota.ScopeUser && actor.CanAdmin("users")) ||
		(scope == quota.ScopeGroup && actor.CanAdmin("groups"))
}

func (h *Handlers) visibleSettings(account user.User) map[string]string {
	values := redacted(h.settings.All())
	for key := range values {
		if !hasPermission(account, settingPermission(key)) {
			delete(values, key)
		}
	}
	return values
}

// Forms need names from neighbouring pages. This endpoint deliberately
// returns just selector data, without granting access to those pages' records.
func (h *Handlers) references(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	out := map[string]any{}
	// Invites is here because a code can carry a group, and an operator
	// trusted with invites alone must be able to pick one.
	if hasPermission(actor, "users,models,usage,security,settings,groups,invites") {
		groups, err := h.groups.List(r.Context(), nil)
		if err != nil {
			return httpx.Internal(err)
		}
		items := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			items = append(items, map[string]any{"id": g.ID, "name": g.Name})
		}
		out["groups"] = items
	}
	if hasPermission(actor, "groups,settings,security") {
		models, err := h.models.ListAll(r.Context(), "")
		if err != nil {
			return httpx.Internal(err)
		}
		items := make([]map[string]any, 0, len(models))
		for _, m := range models {
			items = append(items, map[string]any{"id": m.ID, "display_name": m.DisplayName, "model_id": m.ModelID, "enabled": m.Enabled, "provider_name": m.ProviderName})
		}
		out["models"] = items
	}
	if actor.CanAdmin("models") {
		providers, err := h.providers.List(r.Context())
		if err != nil {
			return httpx.Internal(err)
		}
		items := make([]map[string]any, 0, len(providers))
		for _, p := range providers {
			items = append(items, map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind, "enabled": p.Enabled})
		}
		out["providers"] = items
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handlers) listMemberOptions(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	accounts, total, err := h.users.List(r.Context(), user.ListFilter{
		Search: query.Get("q"), GroupID: query.Get("group_id"),
		Limit: intParam(query.Get("limit"), 20), Offset: intParam(query.Get("offset"), 0),
	})
	if err != nil {
		return httpx.Internal(err)
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, map[string]any{"id": account.ID, "username": account.Username, "nickname": account.Nickname, "group_id": account.GroupID, "group_expires_at": account.GroupExpiresAt})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": items, "total": total})
}
