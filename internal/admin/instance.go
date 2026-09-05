package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
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
		// Whether this instance can post mail at all. The verification
		// setting is inert without it, and the form says so rather than
		// letting an operator switch on something that does nothing.
		"mail_configured": h.auth.MailConfigured(),
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
	settings.RequireEmail:         true,
	settings.VerifyEmail:          true,
	settings.EmailDomains:         true,
	settings.SignupsPerMinute:     true,
	settings.SignupsPerHour:       true,
	settings.AdminsBypassQuota:    true,
	settings.UsageDisplay:         true,
	settings.LandingMode:          true,
	settings.LandingIntro:         true,
	settings.TrialEnabled:         true,
	settings.TrialTurns:           true,
	settings.TrialModel:           true,
	settings.DefaultSystemPrompt:  true,
	settings.ConversationMaxTurns: true,
	settings.APIEnabled:           true,
	settings.AttachmentMaxMB:      true,
	settings.AttachmentRetain:     true,
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

	if mode, present := body[settings.LandingMode]; present && !settings.ValidLandingMode(mode) {
		return httpx.BadRequest("Unknown landing mode %q.", mode)
	}
	// A trial model that does not exist would make the front door offer a
	// conversation it cannot hold.
	if modelID, present := body[settings.TrialModel]; present && modelID != "" {
		if !isValidID(modelID) {
			return httpx.BadRequest("Malformed model id.")
		}
		if _, err := h.models.ByID(r.Context(), modelID); err != nil {
			return model.TranslateError(err)
		}
	}

	// An unknown display mode would leave every user's allowance rendered as
	// nothing at all, so it is checked here rather than guessed at in the
	// browser.
	if raw, present := body[settings.AttachmentMaxMB]; present {
		size, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || size < 1 || size > settings.MaxAttachmentCeilingMB {
			return httpx.BadRequest("The attachment limit must be between 1 and %d MB.",
				settings.MaxAttachmentCeilingMB)
		}
	}
	if display, present := body[settings.UsageDisplay]; present && !settings.ValidUsageDisplay(display) {
		return httpx.BadRequest("Unknown usage display %q.", display)
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

// --- settings as a document ---------------------------------------------------

// importSettings applies an exported settings document.
//
// Deliberately more forgiving than the ordinary save above. An export usually
// comes from another instance, where two of the values are row identifiers —
// the default registration group and the trial model — that mean nothing
// here. Rejecting the whole file for those would make the feature useless
// exactly when it is wanted, so they are dropped and named in the response;
// everything else is applied.
//
// Unknown keys are skipped rather than refused for the same reason: a
// document written by a newer release should not be unusable by this one.
func (h *Handlers) importSettings(w http.ResponseWriter, r *http.Request) error {
	var body map[string]string
	if err := httpx.DecodeJSON(w, r, &body, 256*1024); err != nil {
		return err
	}
	if len(body) == 0 {
		return httpx.BadRequest("That file contains no settings.")
	}

	applied := map[string]string{}
	skipped := []string{}

	for key, value := range body {
		if !writableSettings[key] || len(value) > 8*1024 {
			skipped = append(skipped, key)
			continue
		}
		applied[key] = value
	}

	if mode, present := applied[settings.LandingMode]; present && !settings.ValidLandingMode(mode) {
		delete(applied, settings.LandingMode)
		skipped = append(skipped, settings.LandingMode)
	}
	if display, present := applied[settings.UsageDisplay]; present && !settings.ValidUsageDisplay(display) {
		delete(applied, settings.UsageDisplay)
		skipped = append(skipped, settings.UsageDisplay)
	}
	if raw, present := applied[settings.AttachmentMaxMB]; present {
		size, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || size < 1 || size > settings.MaxAttachmentCeilingMB {
			delete(applied, settings.AttachmentMaxMB)
			skipped = append(skipped, settings.AttachmentMaxMB)
		}
	}

	// The two identifiers. A dangling one is cleared rather than carried, so
	// the instance ends up in a state it can describe: "no default group"
	// beats "a default group that does not exist".
	if modelID, present := applied[settings.TrialModel]; present && modelID != "" {
		if !isValidID(modelID) {
			applied[settings.TrialModel] = ""
			skipped = append(skipped, settings.TrialModel)
		} else if _, err := h.models.ByID(r.Context(), modelID); err != nil {
			applied[settings.TrialModel] = ""
			skipped = append(skipped, settings.TrialModel)
		}
	}
	if groupID, present := applied[settings.RegistrationGroup]; present && groupID != "" {
		if !isValidID(groupID) {
			applied[settings.RegistrationGroup] = ""
			skipped = append(skipped, settings.RegistrationGroup)
		} else if _, err := h.groups.ByID(r.Context(), nil, groupID); err != nil {
			applied[settings.RegistrationGroup] = ""
			skipped = append(skipped, settings.RegistrationGroup)
		}
	}

	if err := h.settings.SetMany(r.Context(), applied); err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"settings": h.settings.All(),
		"applied":  len(applied),
		"skipped":  skipped,
	})
}
