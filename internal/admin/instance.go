package admin

import (
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The dashboard and the instance settings.

// dashboard is deliberately short. An operator opening it wants to know
// whether the thing is working and what it is costing — not to read a wall of
// charts that exist because a dashboard is expected to have charts.
func (h *Handlers) dashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	now := time.Now()

	users, totalUsers, err := h.users.List(ctx, user.ListFilter{Limit: 5})
	if err != nil {
		return httpx.Internal(err)
	}
	_, activeUsers, err := h.users.List(ctx, user.ListFilter{Status: user.StatusActive, Limit: 1})
	if err != nil {
		return httpx.Internal(err)
	}

	providers, err := h.providers.List(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	models, err := h.models.ListAll(ctx, "")
	if err != nil {
		return httpx.Internal(err)
	}

	enabledModels := 0
	for _, model := range models {
		if model.Enabled {
			enabledModels++
		}
	}
	enabledProviders := 0
	for _, provider := range providers {
		if provider.Enabled {
			enabledProviders++
		}
	}

	day := usage.Filter{Since: now.Add(-24 * time.Hour).UnixMilli()}
	week := usage.Filter{Since: now.AddDate(0, 0, -7).UnixMilli()}

	today, err := h.usage.Totals(ctx, day)
	if err != nil {
		return httpx.Internal(err)
	}
	thisWeek, err := h.usage.Totals(ctx, week)
	if err != nil {
		return httpx.Internal(err)
	}
	topModels, err := h.usage.GroupBy(ctx, "model", week)
	if err != nil {
		return httpx.Internal(err)
	}
	series, err := h.usage.Series(ctx, week, 6*time.Hour)
	if err != nil {
		return httpx.Internal(err)
	}
	recent, _, err := h.usage.List(ctx, usage.Filter{Limit: 8})
	if err != nil {
		return httpx.Internal(err)
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"counts": map[string]any{
			"users":             totalUsers,
			"active_users":      activeUsers,
			"providers":         len(providers),
			"enabled_providers": enabledProviders,
			"models":            len(models),
			"enabled_models":    enabledModels,
		},
		"newest_users": users,
		"last_24h":     today,
		"last_7d":      thisWeek,
		"top_models":   topModels,
		"series":       series,
		"bucket_ms":    (6 * time.Hour).Milliseconds(),
		"recent":       recent,
	})
}

func (h *Handlers) listSettings(w http.ResponseWriter, r *http.Request) error {
	groups, err := h.groups.List(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	// The groups travel with the settings because one of the settings is
	// which group new accounts join, and a select needs its options.
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"settings": h.settings.All(),
		"groups":   groups,
	})
}

// Only these keys can be written. An open key/value endpoint would let an
// administrator invent settings nothing reads, and would let a typo silently
// replace a real one.
var writableSettings = map[string]bool{
	settings.SiteName:             true,
	settings.SiteDescription:      true,
	settings.RegistrationEnabled:  true,
	settings.RegistrationGroup:    true,
	settings.AdminsBypassQuota:    true,
	settings.DefaultSystemPrompt:  true,
	settings.ConversationMaxTurns: true,
}

func (h *Handlers) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var body map[string]string
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}

	for key, value := range body {
		if !writableSettings[key] {
			return httpx.BadRequest("Unknown setting %q.", key)
		}
		if len(value) > 8*1024 {
			return httpx.BadRequest("Setting %q is too long.", key)
		}
	}

	// A registration group that does not exist would send every new account
	// into no group at all, which quietly means no models.
	if groupID, present := body[settings.RegistrationGroup]; present && groupID != "" {
		if !isValidID(groupID) {
			return httpx.BadRequest("Malformed group id.")
		}
		if _, err := h.groups.ByID(r.Context(), nil, groupID); err != nil {
			return translateGroupError(err)
		}
	}

	if err := h.settings.SetMany(r.Context(), body); err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"settings": h.settings.All()})
}
