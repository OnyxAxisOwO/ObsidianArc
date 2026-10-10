package admin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/screening"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The dashboard and the instance settings.

// dashboardAccount is the only account shape the dashboard sends. The records
// it lists come from the same table the users grant reads, and that grant is
// the one that may see an address, the signup address or a ban reason; the
// dashboard grant is narrower, so the fields are named here rather than taken
// from user.User, whose JSON carries all of them.
type dashboardAccount struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	CreatedAt int64  `json:"created_at"`
}

// dashboard is deliberately short. An operator opening it wants to know
// whether the thing is working and what it is costing — not to read a wall of
// charts that exist because a dashboard is expected to have charts.
func (h *Handlers) dashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	now := time.Now()
	zone, err := zoneFrom(r)
	if err != nil {
		return err
	}

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
	// The day before that, so each of the day's figures can say which way it
	// moved. A figure with no direction is a number to memorise; one with an
	// arrow is a thing to act on.
	dayBefore := usage.Filter{Since: now.Add(-48 * time.Hour).UnixMilli(), Until: day.Since}
	week := usage.Filter{Since: now.AddDate(0, 0, -7).UnixMilli()}

	metric := r.URL.Query().Get("metric")
	var (
		today, yesterday, thisWeek usage.Totals
		topModels, topUsers        []usage.Breakdown
		series                     []usage.Point
		heatmap                    []usage.Slot
		recent                     []usage.Record
	)
	err = together(
		func() (err error) { today, err = h.usage.Totals(ctx, day); return },
		func() (err error) { yesterday, err = h.usage.Totals(ctx, dayBefore); return },
		func() (err error) { thisWeek, err = h.usage.Totals(ctx, week); return },
		func() (err error) { topModels, err = h.usage.GroupBy(ctx, "model", metric, week); return },
		func() (err error) { topUsers, err = h.usage.GroupBy(ctx, "user", metric, week); return },
		func() (err error) { series, err = h.usage.Series(ctx, week, 6*time.Hour, zone); return },
		// When in the week people use it: the shape an operator plans
		// maintenance around, and the one a total over seven days hides.
		func() (err error) { heatmap, err = h.usage.Heatmap(ctx, week, zone); return },
		func() (err error) { recent, _, err = h.usage.List(ctx, usage.Filter{Limit: 8}); return },
	)
	if err != nil {
		return httpx.Internal(err)
	}

	newest := make([]dashboardAccount, 0, len(users))
	for _, account := range users {
		newest = append(newest, dashboardAccount{
			ID: account.ID, Username: account.Username, Nickname: account.Nickname, CreatedAt: account.CreatedAt,
		})
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
		"newest_users": newest,
		"last_24h":     today,
		"prev_24h":     yesterday,
		"last_7d":      thisWeek,
		"top_models":   topModels,
		"top_users":    topUsers,
		"series":       series,
		"bucket_ms":    (6 * time.Hour).Milliseconds(),
		"heatmap":      heatmap,
		"recent":       recent,
	})
}

func (h *Handlers) listSettings(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	out := map[string]any{"settings": h.visibleSettings(actor)}
	if actor.CanAdmin("security") {
		out["signup_review_prompt_default"] = screening.DefaultPrompt()
		groups, err := h.groups.List(r.Context(), nil)
		if err != nil {
			return httpx.Internal(err)
		}
		// Registration needs a default-group selector, not the groups' policies.
		options := make([]map[string]string, 0, len(groups))
		for _, g := range groups {
			options = append(options, map[string]string{"id": g.ID, "name": g.Name})
		}
		out["groups"] = options
		out["mail_configured"] = h.auth.MailConfigured()
	}
	if actor.CanAdmin("settings") {
		held, bytes, err := h.conversations.Held(r.Context())
		if err != nil {
			return httpx.Internal(err)
		}
		out["attachments"] = map[string]any{"held": held, "bytes": bytes}
		if h.settings != nil {
			out["backgrounds"] = auth.BackgroundURLs(h.settings)
			if h.settings.SiteLogoUpdatedAt() > 0 {
				out["logo_url"] = fmt.Sprintf("/api/site/logo?v=%d", h.settings.SiteLogoUpdatedAt())
			} else {
				out["logo_url"] = ""
			}
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// Only these keys can be written. An open key/value endpoint would let an
// administrator invent settings nothing reads, and would let a typo silently
// replace a real one.
var writableSettings = map[string]bool{
	settings.SiteName:                   true,
	settings.SiteDescription:            true,
	settings.SiteAuthCardPosition:       true,
	settings.ThemeMode:                  true,
	settings.ThemeAccent:                true,
	settings.ThemeCustomAccent:          true,
	settings.ThemeBackgroundTint:        true,
	settings.ThemeWallpaperDim:          true,
	settings.ThemeWallpaperBlur:         true,
	settings.ThemeTranslucency:          true,
	settings.ThemePanelBlur:             true,
	settings.ThemeEnforce:               true,
	settings.AboutTitle:                 true,
	settings.AboutBody:                  true,
	settings.AboutShowSoftwareInfo:      true,
	settings.UpdateCheck:                true,
	settings.HomeNotice:                 true,
	settings.HomeNoticeDismissible:      true,
	settings.HomeNoticeBody:             true,
	settings.HomeNoticeTone:             true,
	settings.RegistrationEnabled:        true,
	settings.RegistrationGroup:          true,
	settings.RequireEmail:               true,
	settings.VerifyEmail:                true,
	settings.EmailDomains:               true,
	settings.SignupsPerMinute:           true,
	settings.SignupsPerHour:             true,
	settings.SignupsPerIP:               true,
	settings.SignupsIPWindowMin:         true,
	settings.InvitesRequired:            true,
	settings.InvitesUserEnabled:         true,
	settings.InvitesUserLimit:           true,
	settings.InvitesRewardCards:         true,
	settings.InvitesRewardCardDays:      true,
	settings.InvitesRewardEvery:         true,
	settings.TurnstileSiteKey:           true,
	settings.TurnstileSecretKey:         true,
	settings.TurnstileOnLogin:           true,
	settings.TurnstileOnSignup:          true,
	settings.TurnstileOnAPIKey:          true,
	settings.TurnstileOnRedeem:          true,
	settings.TurnstileOnFeedback:        true,
	settings.TurnstileOnImages:          true,
	settings.PoWOnImages:                true,
	settings.RegistrationCaptchaMode:    true,
	settings.PoWBaseMaxNumber:           true,
	settings.PoWElevatedMaxNumber:       true,
	settings.PoWThreshold:               true,
	settings.FeedbackShowStaffName:      true,
	settings.OAuthGitHubEnabled:         true,
	settings.OAuthGitHubID:              true,
	settings.OAuthGitHubSecret:          true,
	settings.OAuthGoogleEnabled:         true,
	settings.OAuthGoogleID:              true,
	settings.OAuthGoogleSecret:          true,
	settings.OAuthOIDCEnabled:           true,
	settings.OAuthOIDCClientID:          true,
	settings.OAuthOIDCClientSecret:      true,
	settings.OAuthOIDCIssuer:            true,
	settings.OAuthOIDCDisplayName:       true,
	settings.OAuthOIDCScopes:            true,
	settings.OAuthOIDCAuthURL:           true,
	settings.OAuthOIDCTokenURL:          true,
	settings.OAuthOIDCUserInfoURL:       true,
	settings.OAuthOIDCTrustEmail:        true,
	settings.OAuthThirdPartyOnlySignup:  true,
	settings.OAuthOIDCOnlySignup:        true,
	settings.OAuthOIDCRequireForAll:     true,
	settings.OAuthAllowSignup:           true,
	settings.OAuthAllowPassword:         true,
	settings.OAuthRequirePassword:       true,
	settings.OAuthRequireUsername:       true,
	settings.OAuthOIDCRequireCompletion: true,
	settings.OAuthLinkByEmail:           true,
	settings.SignupReview:               true,
	settings.SignupReviewModel:          true,
	settings.SignupReviewMode:           true,
	settings.SignupReviewPrompt:         true,
	settings.SignupReviewRefusal:        true,
	settings.SignupReviewRestrictHours:  true,
	settings.TwoFactorPolicy:            true,
	settings.TwoFactorIssuer:            true,
	settings.TwoFactorRememberDays:      true,
	settings.TwoFactorPluginManage:      true,
	settings.TwoFactorBackofficeMode:    true,
	settings.TwoFactorBackofficeMinutes: true,
	settings.TwoFactorBackofficeNetwork: true,
	settings.TwoFactorBackofficeBrowser: true,
	settings.NewDeviceEmail:             true,
	settings.ChatChallengeRequests:      true,
	settings.ChatChallengeWindowSecs:    true,
	settings.ChatChallengeClearMins:     true,
	settings.AdminsBypassQuota:          true,
	settings.QuotaMaxConcurrent:         true,
	settings.HealthProbe:                true,
	settings.HealthWindowMins:           true,
	settings.HealthDisableAfter:         true,
	settings.HealthRetainDays:           true,
	settings.HealthDisableBelow:         true,
	settings.HealthShowUsers:            true,
	settings.HealthWarnBelow:            true,
	settings.HealthResetAt:              true,
	settings.UsageDisplay:               true,
	settings.LandingMode:                true,
	settings.LandingIntro:               true,
	settings.TrialEnabled:               true,
	settings.TrialTurns:                 true,
	settings.TrialModel:                 true,
	settings.DefaultSystemPrompt:        true,
	settings.ConversationMaxTurns:       true,
	settings.APIEnabled:                 true,
	settings.AttachmentMaxMB:            true,
	settings.AttachmentRetain:           true,
	settings.ImageHistory:               true,
	settings.AttachmentPurgeDays:        true,
	settings.AttachmentPurgeDaily:       true,
	settings.AttachmentOrphanMins:       true,
	settings.SiteBrowserTitle:           true,
	settings.PWAName:                    true,
	settings.PWAShortName:               true,
	settings.PWADescription:             true,
	settings.PWAThemeColor:              true,
	settings.PWABackgroundColor:         true,
	settings.PWAIconURL:                 true,
	settings.LeaderboardShowUsers:       true,
	settings.LeaderboardIdentity:        true,
	settings.LeaderboardSize:            true,
	settings.LeaderboardShowModels:      true,
}

// The numeric settings and what they may be, shared by the ordinary save and
// the import below so the two cannot come to disagree about a bound. The
// browser offers the same range, but that is a convenience: this is the check.
var numericBounds = map[string][2]int{
	settings.SignupReviewRestrictHours:  {0, 24 * 365},
	settings.InvitesUserLimit:           {0, 10000},
	settings.InvitesRewardCards:         {0, 100},
	settings.InvitesRewardCardDays:      {1, 3650},
	settings.InvitesRewardEvery:         {1, 1000},
	settings.ChatChallengeRequests:      {0, 1000},
	settings.ChatChallengeWindowSecs:    {5, 3600},
	settings.ChatChallengeClearMins:     {1, 24 * 60},
	settings.TwoFactorRememberDays:      {0, settings.MaxTwoFactorRememberDays},
	settings.TwoFactorBackofficeMinutes: {1, settings.MaxTwoFactorBackofficeMinutes},
	settings.LeaderboardSize:            {1, settings.MaxLeaderboardSize},
	// The ranges the account's own wallpaper sliders have (theme.ts clamps
	// to the same), so a site default can be nothing a reader could not
	// have picked for themselves.
	settings.ThemeWallpaperDim:  {0, 100},
	settings.ThemeWallpaperBlur: {0, 40},
	settings.ThemeTranslucency:  {0, 90},
	settings.ThemePanelBlur:     {0, 40},
}

func (h *Handlers) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var body map[string]string
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}

	twoFactorCode := body["two_factor_code"]
	delete(body, "two_factor_code")
	if twoFactorCode == "" {
		twoFactorCode = r.Header.Get("X-Two-Factor-Code")
	}

	for key, value := range body {
		if !hasPermission(auth.MustUser(r.Context()), h.settingPermission(key)) {
			return permissionDenied()
		}
		if !h.writable(key) {
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
	for _, key := range h.secretKeys() {
		if value, present := body[key]; present && (value == "" || value == secretMask) {
			delete(body, key)
		}
	}

	if mode, present := body[settings.LandingMode]; present && !settings.ValidLandingMode(mode) {
		return httpx.BadRequest("Unknown landing mode %q.", mode)
	}
	if pos, present := body[settings.SiteAuthCardPosition]; present && !settings.ValidAuthCardPosition(pos) {
		return httpx.BadRequest("Unknown auth card position %q.", pos)
	}
	if err := checkThemeSettings(body); err != nil {
		return err
	}
	if err := h.checkOIDCSettings(body); err != nil {
		return err
	}
	if err := checkTwoFactorSettings(auth.MustUser(r.Context()), body); err != nil {
		return err
	}
	if newPrompt, present := body[settings.SignupReviewPrompt]; present {
		currentPrompt := h.settings.Get(settings.SignupReviewPrompt)
		if strings.TrimSpace(newPrompt) != strings.TrimSpace(currentPrompt) {
			actor := auth.MustUser(r.Context())
			if !actor.TwoFactorEnabled() {
				return httpx.ForbiddenCode("two_factor_required",
					"Modifying the AI review prompt requires two-step verification enabled on your account.")
			}
			if strings.TrimSpace(twoFactorCode) == "" {
				return httpx.BadRequestCode("two_factor_code_required",
					"Enter a code from your authenticator app to modify the AI review prompt.")
			}
			if err := h.auth.VerifyTwoFactorCode(r.Context(), actor, strings.TrimSpace(twoFactorCode), h.clientIP(r)); err != nil {
				return auth.TranslateTwoFactorError(w, err)
			}
		}
	}
	// Empty is allowed for both — it means "no override", not "black" — so
	// only a non-empty value that fails the format is refused.
	for _, key := range []string{settings.PWAThemeColor, settings.PWABackgroundColor} {
		if value, present := body[key]; present && value != "" && !settings.ValidHexColor(value) {
			return httpx.BadRequest("Setting %q must be a #rrggbb colour.", key)
		}
	}
	// A manifest icon on another host is silently dropped by the image
	// policy anyway; refusing it here says so instead of leaving an operator
	// to wonder why the install prompt has no icon.
	if url, present := body[settings.PWAIconURL]; present && !settings.ValidPWAIconURL(url) {
		return httpx.BadRequest("The PWA icon must be a path on this server or a data URI.")
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
	for key, bounds := range numericBounds {
		if raw, present := body[key]; present {
			value, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || value < bounds[0] || value > bounds[1] {
				return httpx.BadRequest("Setting %q must be between %d and %d.",
					key, bounds[0], bounds[1])
			}
		}
	}

	if display, present := body[settings.UsageDisplay]; present && !settings.ValidUsageDisplay(display) {
		return httpx.BadRequest("Unknown usage display %q.", display)
	}
	// The notice is served to people who have not signed in, so how much of it
	// there can be is decided here and not by whatever the form happened to send.
	if tone, present := body[settings.HomeNoticeTone]; present && !settings.ValidHomeNoticeTone(tone) {
		return httpx.BadRequest("Unknown notice tone %q.", tone)
	}
	if text, present := body[settings.HomeNotice]; present && utf8.RuneCountInString(text) > settings.MaxHomeNoticeTitleChars {
		return httpx.BadRequest("The notice line is at most %d characters; put the rest in the text that opens from it.",
			settings.MaxHomeNoticeTitleChars)
	}
	if text, present := body[settings.HomeNoticeBody]; present && utf8.RuneCountInString(text) > settings.MaxHomeNoticeBodyChars {
		return httpx.BadRequest("The notice text is at most %d characters.", settings.MaxHomeNoticeBodyChars)
	}
	if identity, present := body[settings.LeaderboardIdentity]; present && !settings.ValidLeaderboardIdentity(identity) {
		return httpx.BadRequest("Unknown leaderboard identity %q.", identity)
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

	if err := h.validateDefined(body); err != nil {
		return err
	}

	if err := h.settings.SetMany(r.Context(), body); err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"settings": h.visibleSettings(auth.MustUser(r.Context()))})
}

// --- settings as a document ---------------------------------------------------

// importSettings applies an exported settings document.
//
// Deliberately more forgiving than the ordinary save above. An export usually
// comes from another instance, where some of the values are row identifiers —
// the default registration group, the trial model, and the model the sign-up
// reviewer asks — that mean nothing here. Rejecting the whole file for those
// would make the feature useless exactly when it is wanted, so they are
// dropped and named in the response; everything else is applied.
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

	twoFactorCode := body["two_factor_code"]
	delete(body, "two_factor_code")
	if twoFactorCode == "" {
		twoFactorCode = r.Header.Get("X-Two-Factor-Code")
	}

	applied := map[string]string{}
	skipped := []string{}

	for key, value := range body {
		if !hasPermission(auth.MustUser(r.Context()), h.settingPermission(key)) {
			return permissionDenied()
		}
		if !h.writable(key) || len(value) > 8*1024 {
			skipped = append(skipped, key)
			continue
		}
		applied[key] = value
	}

	// The same rule the form's save keeps: an export shows a secret as the
	// mask, and importing that file back used to write the row of dots over
	// the real value — every challenge or provider sign-in behind it then
	// failing for everybody, with nothing on screen to say why.
	for _, key := range h.secretKeys() {
		if value, present := applied[key]; present && (value == "" || value == secretMask) {
			delete(applied, key)
		}
	}

	if mode, present := applied[settings.LandingMode]; present && !settings.ValidLandingMode(mode) {
		delete(applied, settings.LandingMode)
		skipped = append(skipped, settings.LandingMode)
	}
	if pos, present := applied[settings.SiteAuthCardPosition]; present && !settings.ValidAuthCardPosition(pos) {
		delete(applied, settings.SiteAuthCardPosition)
		skipped = append(skipped, settings.SiteAuthCardPosition)
	}
	for _, key := range themeChecked {
		if value, present := applied[key]; present && checkThemeSettings(map[string]string{key: value}) != nil {
			delete(applied, key)
			skipped = append(skipped, key)
		}
	}
	if display, present := applied[settings.UsageDisplay]; present && !settings.ValidUsageDisplay(display) {
		delete(applied, settings.UsageDisplay)
		skipped = append(skipped, settings.UsageDisplay)
	}
	if tone, present := applied[settings.HomeNoticeTone]; present && !settings.ValidHomeNoticeTone(tone) {
		delete(applied, settings.HomeNoticeTone)
		skipped = append(skipped, settings.HomeNoticeTone)
	}
	if identity, present := applied[settings.LeaderboardIdentity]; present && !settings.ValidLeaderboardIdentity(identity) {
		delete(applied, settings.LeaderboardIdentity)
		skipped = append(skipped, settings.LeaderboardIdentity)
	}
	for _, key := range []string{settings.PWAThemeColor, settings.PWABackgroundColor} {
		if value, present := applied[key]; present && value != "" && !settings.ValidHexColor(value) {
			delete(applied, key)
			skipped = append(skipped, key)
		}
	}
	if url, present := applied[settings.PWAIconURL]; present && !settings.ValidPWAIconURL(url) {
		delete(applied, settings.PWAIconURL)
		skipped = append(skipped, settings.PWAIconURL)
	}
	// Dropped and named, like the rest of this document, rather than failing the
	// whole import: a file exported before this check existed can carry one.
	for _, key := range oidcURLKeys {
		if h.oidcURLRefused(key, applied[key]) {
			delete(applied, key)
			skipped = append(skipped, key)
		}
	}
	// Dropped rather than refused, like everything else here — and for the
	// importer's own sake: a file from an instance that requires the second
	// step must not shut out an operator who has not switched it on yet.
	for _, key := range []string{settings.TwoFactorPolicy, settings.TwoFactorIssuer, settings.TwoFactorBackofficeMode} {
		if value, present := applied[key]; present {
			if checkTwoFactorSettings(auth.MustUser(r.Context()), map[string]string{key: value}) != nil {
				delete(applied, key)
				skipped = append(skipped, key)
			}
		}
	}
	if value, present := applied[settings.SignupReviewPrompt]; present && strings.TrimSpace(value) != strings.TrimSpace(h.settings.Get(settings.SignupReviewPrompt)) {
		actor := auth.MustUser(r.Context())
		if !actor.TwoFactorEnabled() || strings.TrimSpace(twoFactorCode) == "" ||
			h.auth.VerifyTwoFactorCode(r.Context(), actor, strings.TrimSpace(twoFactorCode), h.clientIP(r)) != nil {
			delete(applied, settings.SignupReviewPrompt)
			skipped = append(skipped, settings.SignupReviewPrompt)
		}
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
	for key, bounds := range numericBounds {
		if raw, present := applied[key]; present {
			value, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || value < bounds[0] || value > bounds[1] {
				delete(applied, key)
				skipped = append(skipped, key)
			}
		}
	}

	// The identifiers that name a model, and the one that names a group. A
	// dangling one is cleared rather than carried, so the instance ends up in
	// a state it can describe: "no default group" beats "a default group that
	// does not exist". The list is a loop rather than a block each so that a
	// fourth identifier is added in one place.
	for _, key := range []string{settings.TrialModel, settings.SignupReviewModel} {
		modelID, present := applied[key]
		if !present || modelID == "" {
			continue
		}
		if !isValidID(modelID) {
			applied[key] = ""
			skipped = append(skipped, key)
			continue
		}
		if _, err := h.models.ByID(r.Context(), modelID); err != nil {
			applied[key] = ""
			skipped = append(skipped, key)
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

	for key, value := range applied {
		if d, ok := h.settings.Defined(key); ok && d.Validate != nil && d.Validate(value) != nil {
			delete(applied, key)
			skipped = append(skipped, key)
		}
	}

	if err := h.settings.SetMany(r.Context(), applied); err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"settings": h.visibleSettings(auth.MustUser(r.Context())),
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

var themeChecked = []string{settings.ThemeMode, settings.ThemeAccent, settings.ThemeBackgroundTint, settings.ThemeCustomAccent}

// checkThemeSettings holds the theme keys to the shapes the browser reads.
// Only the keys present are checked; a missing one is not being changed.
func checkThemeSettings(body map[string]string) error {
	if mode, present := body[settings.ThemeMode]; present && !settings.ValidThemeMode(mode) {
		return httpx.BadRequest("Unknown theme mode %q.", mode)
	}
	for _, key := range []string{settings.ThemeAccent, settings.ThemeBackgroundTint} {
		if value, present := body[key]; present && !settings.ValidThemeAccent(value) {
			return httpx.BadRequest("Setting %q must be an accent's name.", key)
		}
	}
	if value, present := body[settings.ThemeCustomAccent]; present && value != "" && !settings.ValidHexColor(value) {
		return httpx.BadRequest("Setting %q must be a #rrggbb colour.", settings.ThemeCustomAccent)
	}
	return nil
}

// The OpenID Connect addresses a sign-in reaches, and the issuer discovery is
// fetched from. See settings.ValidOIDCURL.
var oidcURLKeys = []string{
	settings.OAuthOIDCIssuer,
	settings.OAuthOIDCAuthURL,
	settings.OAuthOIDCTokenURL,
	settings.OAuthOIDCUserInfoURL,
}

// oidcURLRefused reports whether saving value under key would store an address
// a sign-in may not use. Trimmed the way the sign-in reads the setting, so what
// is checked is what would be used. A value already stored is not refused here:
// keeping it changes nothing, the sign-in refuses it at use, and refusing it on
// every save would block each other change on the page for an address nobody
// typed this time.
func (h *Handlers) oidcURLRefused(key, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || settings.ValidOIDCURL(value) {
		return false
	}
	return value != strings.TrimSpace(h.settings.Get(key))
}

// checkOIDCSettings refuses a new plaintext OpenID Connect address when it is
// saved. The code lets the screen say which field to change, and the setting
// travels in details because the message beside it is English only. An empty
// value is allowed: it clears the override.
func (h *Handlers) checkOIDCSettings(body map[string]string) error {
	for _, key := range oidcURLKeys {
		if h.oidcURLRefused(key, body[key]) {
			return httpx.BadRequestCode("oidc_url_not_https",
				"Setting %q must be an https address; plain http is allowed only for a loopback address.", key).
				WithDetails(map[string]any{"setting": key})
		}
	}
	return nil
}

func (h *Handlers) putLoginBackground(w http.ResponseWriter, r *http.Request) error {
	variant := settings.NormalizeVariant(r.PathValue("variant"))
	if !settings.ValidLoginBackgroundVariants[variant] {
		return httpx.BadRequest("Unknown login background variant.")
	}

	var body struct {
		Mime string `json:"mime"`
		Data string `json:"data"`
		// A page instead of a picture. Present means this is one; the two
		// are not combined.
		HTML *string `json:"html"`
	}
	if err := httpx.DecodeJSON(w, r, &body, settings.MaxLoginBackgroundBytes*4/3+16*1024); err != nil {
		return err
	}

	if body.HTML != nil {
		at, err := h.settings.SetHTMLBackground(r.Context(), variant, *body.HTML)
		switch {
		case errors.Is(err, settings.ErrLoginBackgroundTooLarge):
			return httpx.BadRequest("That page is too large.")
		case errors.Is(err, settings.ErrLoginBackgroundUnsupported):
			return httpx.BadRequest("The page is empty.")
		case err != nil:
			return httpx.Internal(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"url":        fmt.Sprintf("/api/site/login-background/%s?v=%d", variant, at),
			"html":       true,
			"updated_at": at,
		})
	}

	raw := body.Data
	if comma := strings.Index(raw, ","); comma != -1 && strings.Contains(raw[:comma], "base64") {
		raw = raw[comma+1:]
	}
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, "\n", "")
	raw = strings.ReplaceAll(raw, "\r", "")
	raw = strings.ReplaceAll(raw, "\t", "")
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return httpx.BadRequest("Image data is not valid base64.")
	}

	at, err := h.settings.SetLoginBackground(r.Context(), variant, body.Mime, data)
	if err != nil {
		switch {
		case errors.Is(err, settings.ErrLoginBackgroundUnsupported):
			return httpx.BadRequest("Login backgrounds must be JPEG, PNG, WebP or AVIF.")
		case errors.Is(err, settings.ErrLoginBackgroundTooLarge):
			return httpx.BadRequest("That image is too large.")
		default:
			return httpx.Internal(err)
		}
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"url":        fmt.Sprintf("/api/site/login-background/%s?v=%d", variant, at),
		"updated_at": at,
	})
}

func (h *Handlers) deleteLoginBackground(w http.ResponseWriter, r *http.Request) error {
	variant := settings.NormalizeVariant(r.PathValue("variant"))
	if !settings.ValidLoginBackgroundVariants[variant] {
		return httpx.BadRequest("Unknown login background variant.")
	}

	if err := h.settings.DeleteLoginBackground(r.Context(), variant); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) putLogo(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Mime string `json:"mime"`
		Data string `json:"data"`
	}
	if err := httpx.DecodeJSON(w, r, &body, settings.MaxLogoBytes*4/3+16*1024); err != nil {
		return err
	}

	raw := body.Data
	if comma := strings.Index(raw, ","); comma != -1 && strings.Contains(raw[:comma], "base64") {
		raw = raw[comma+1:]
	}
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, "\n", "")
	raw = strings.ReplaceAll(raw, "\r", "")
	raw = strings.ReplaceAll(raw, "\t", "")
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return httpx.BadRequest("Image data is not valid base64.")
	}

	at, err := h.settings.SetSiteLogo(r.Context(), body.Mime, data)
	if err != nil {
		switch {
		case errors.Is(err, settings.ErrLogoUnsupported):
			return httpx.BadRequest("Logo must be PNG, JPEG, SVG, WebP, AVIF, GIF or ICO.")
		case errors.Is(err, settings.ErrLogoTooLarge):
			return httpx.BadRequest("That image is too large.")
		default:
			return httpx.Internal(err)
		}
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"url":        fmt.Sprintf("/api/site/logo?v=%d", at),
		"updated_at": at,
	})
}

func (h *Handlers) deleteLogo(w http.ResponseWriter, r *http.Request) error {
	if err := h.settings.DeleteSiteLogo(r.Context()); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

// writable reports whether an administrator may write key: a core key in
// the table above, or one an enabled plugin defined.
func (h *Handlers) writable(key string) bool {
	if writableSettings[key] {
		return true
	}
	_, defined := h.settings.Defined(key)
	return defined
}

// validateDefined runs each plugin setting's own validator. The core keys
// are checked one by one above; a plugin's rules travel with its definition
// so this file never has to learn them.
func (h *Handlers) validateDefined(body map[string]string) error {
	for key, value := range body {
		if d, ok := h.settings.Defined(key); ok && d.Validate != nil {
			if err := d.Validate(value); err != nil {
				return httpx.BadRequest("Setting %q: %s", key, err.Error())
			}
		}
	}
	return nil
}

// secretKeys is every credential this build knows about, core and plugin —
// a switched-off plugin's too: redacting a key nothing shows costs nothing,
// and missing one would hand its credential out.
func secretKeys() []string {
	out := append([]string(nil), secretSettings...)
	for _, d := range settings.AllDefinitions() {
		if d.Secret {
			out = append(out, d.Key)
		}
	}
	return out
}

// secretKeys is the same list for this server: the compiled-in ones, and
// those of plugins installed while it runs.
func (h *Handlers) secretKeys() []string {
	out := append([]string(nil), secretSettings...)
	for _, d := range h.settings.AllDefinitions() {
		if d.Secret {
			out = append(out, d.Key)
		}
	}
	return out
}

// Settings that are credentials. They are written through this endpoint and
// never read back out of it.
var secretSettings = []string{
	settings.TurnstileSecretKey,
	settings.OAuthGitHubSecret,
	settings.OAuthGoogleSecret,
	settings.OAuthOIDCClientSecret,
}

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
func redacted(all map[string]string) map[string]string { return redactedWith(all, secretKeys()) }

func redactedWith(all map[string]string, secrets []string) map[string]string {
	out := make(map[string]string, len(all))
	for key, value := range all {
		out[key] = value
	}
	for _, key := range secrets {
		if out[key] != "" {
			out[key] = secretMask
		}
	}
	return out
}
