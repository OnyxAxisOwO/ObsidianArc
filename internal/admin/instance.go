package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
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
	metric := r.URL.Query().Get("metric")
	topModels, err := h.usage.GroupBy(ctx, "model", metric, week)
	if err != nil {
		return httpx.Internal(err)
	}
	topUsers, err := h.usage.GroupBy(ctx, "user", metric, week)
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
		"top_users":    topUsers,
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
	// What the retention policy is actually holding. Without it an operator
	// has to trust that their cleanup is working rather than see it.
	held, bytes, err := h.conversations.Held(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"settings":    redacted(h.settings.All()),
		"groups":      groups,
		"attachments": map[string]any{"held": held, "bytes": bytes},
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
	settings.SiteName:              true,
	settings.SiteDescription:       true,
	settings.AboutTitle:            true,
	settings.AboutBody:             true,
	settings.HomeNotice:            true,
	settings.HomeNoticeDismissible: true,
	settings.RegistrationEnabled:   true,
	settings.RegistrationGroup:     true,
	settings.RequireEmail:          true,
	settings.QQRequirement:         true,
	settings.VerifyEmail:           true,
	settings.EmailDomains:          true,
	settings.SignupsPerMinute:      true,
	settings.SignupsPerHour:        true,
	settings.SignupsPerIP:          true,
	settings.SignupsIPWindowMin:    true,
	settings.TurnstileSiteKey:      true,
	settings.TurnstileSecretKey:    true,
	settings.TurnstileOnLogin:      true,
	settings.TurnstileOnSignup:     true,
	settings.TurnstileOnAPIKey:     true,
	settings.SignupReview:          true,
	settings.SignupReviewModel:     true,
	settings.SignupReviewMode:      true,
	settings.SignupReviewRefusal:   true,
	settings.AdminsBypassQuota:     true,
	settings.HealthProbe:           true,
	settings.HealthWindowMins:      true,
	settings.HealthDisableAfter:    true,
	settings.HealthRetainDays:      true,
	settings.HealthDisableBelow:    true,
	settings.HealthShowUsers:       true,
	settings.HealthWarnBelow:       true,
	settings.HealthResetAt:         true,
	settings.UsageDisplay:          true,
	settings.LandingMode:           true,
	settings.LandingIntro:          true,
	settings.TrialEnabled:          true,
	settings.TrialTurns:            true,
	settings.TrialModel:            true,
	settings.DefaultSystemPrompt:   true,
	settings.ConversationMaxTurns:  true,
	settings.APIEnabled:            true,
	settings.AttachmentMaxMB:       true,
	settings.AttachmentRetain:      true,
	settings.AttachmentPurgeDays:   true,
	settings.AttachmentPurgeDaily:  true,
	settings.AttachmentOrphanMins:  true,
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

	// The form was shown a mask, so saving it unchanged sends the mask back.
	// Writing that would replace the secret with a row of dots, and the first
	// challenge after would fail for everybody with nothing on screen to say
	// why. An empty value keeps what is stored, the way a provider's API key
	// field does; clearing one is done by switching the challenge off.
	for _, key := range secretSettings {
		if value, present := body[key]; present && (value == "" || value == secretMask) {
			delete(body, key)
		}
	}

	if mode, present := body[settings.LandingMode]; present && !settings.ValidLandingMode(mode) {
		return httpx.BadRequest("Unknown landing mode %q.", mode)
	}
	if req, present := body[settings.QQRequirement]; present && !settings.ValidQQRequirement(req) {
		return httpx.BadRequest("Unknown QQ requirement %q.", req)
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
	// An unparseable schedule would read as "off" and quietly never run, so
	// it is refused here rather than discovered by an operator wondering why
	// nothing is being cleaned up.
	if raw, present := body[settings.AttachmentPurgeDaily]; present && strings.TrimSpace(raw) != "" {
		if _, _, ok := conversation.ParseDailyTime(raw); !ok {
			return httpx.BadRequest("The daily cleanup time must be HH:MM, or empty for never.")
		}
	}
	if raw, present := body[settings.AttachmentPurgeDays]; present {
		if days, err := strconv.Atoi(strings.TrimSpace(raw)); err != nil || days < 0 || days > 3650 {
			return httpx.BadRequest("Keep images for between 0 and 3650 days; 0 means no age limit.")
		}
	}
	if raw, present := body[settings.AttachmentOrphanMins]; present {
		if mins, err := strconv.Atoi(strings.TrimSpace(raw)); err != nil || mins < 5 || mins > 1440 {
			return httpx.BadRequest("Unsent uploads must be kept for between 5 and 1440 minutes.")
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
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"settings": redacted(h.settings.All())})
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
	if raw, present := applied[settings.AttachmentPurgeDaily]; present && strings.TrimSpace(raw) != "" {
		if _, _, ok := conversation.ParseDailyTime(raw); !ok {
			delete(applied, settings.AttachmentPurgeDaily)
			skipped = append(skipped, settings.AttachmentPurgeDaily)
		}
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
		"settings": redacted(h.settings.All()),
		"applied":  len(applied),
		"skipped":  skipped,
	})
}

// purgeAttachments drops every stored image immediately.
//
// The same operation the daily schedule performs, on demand — because an
// operator who has just changed the policy, or who has been asked to delete
// something now, should not have to wait until three in the morning to find
// out whether it works.
//
// It does not touch uploads nobody has sent yet: those belong to a message
// being written, and taking them would break it mid-compose. The orphan
// window is what governs those.
func (h *Handlers) purgeAttachments(w http.ResponseWriter, r *http.Request) error {
	dropped, err := h.conversations.DiscardBefore(r.Context(), time.Now().UnixMilli())
	if err != nil {
		return httpx.Internal(err)
	}

	// Recorded as this cycle's run, so a manual purge at 02:00 does not leave
	// the scheduled one to repeat the same work an hour later.
	if err := h.settings.Set(r.Context(), settings.AttachmentPurgeLast,
		strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
		return httpx.Internal(err)
	}

	held, bytes, err := h.conversations.Held(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"purged":      dropped,
		"attachments": map[string]any{"held": held, "bytes": bytes},
	})
}

// Settings that are credentials. They are written through this endpoint and
// never read back out of it.
var secretSettings = []string{settings.TurnstileSecretKey}

// Enough to show a field is filled in and nothing an attacker could use. A
// provider's API key carries a four-character hint for the same job; a
// Turnstile secret is short enough that even four characters is more than the
// screen needs.
const secretMask = "••••••••"

// redacted copies the settings with every credential masked.
//
// A copy, because All() hands back the live map and masking it in place would
// blank the running configuration. This is the endpoint an administrator
// reads, so the mask is not about them — it is about the response existing at
// all: a secret in a JSON body is a secret in a proxy log, a browser cache
// and whatever the operator pasted the response into.
func redacted(all map[string]string) map[string]string {
	out := make(map[string]string, len(all))
	for key, value := range all {
		out[key] = value
	}
	for _, key := range secretSettings {
		if out[key] != "" {
			out[key] = secretMask
		}
	}
	return out
}
