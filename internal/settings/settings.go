// Package settings is the instance-wide key/value store an administrator can
// change without a restart: whether registration is open, what the site is
// called, which group new accounts join.
//
// The whole table is a handful of rows read on nearly every request, so it is
// cached in memory and refreshed on write. One process owns the database, so
// the cache cannot go stale behind its back.
package settings

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Known keys. Anything not listed here is still storable — the admin UI only
// offers these, and a future module can add its own without a migration.
const (
	SiteName        = "site.name"
	SiteDescription = "site.description"
	// The About panel's heading and Markdown introduction. Empty is the normal
	// state and means "use the instance name and the built-in introduction",
	// so an operator who never opens this screen still gets a sensible page.
	AboutTitle = "about.title"
	AboutBody  = "about.body"
	// A standing notice above the chat. Unlike an announcement, which is a
	// dated thing someone reads once, this is a property of the instance: it
	// stays until an operator takes it down. Empty means there is none.
	HomeNotice = "home.notice"
	// Whether a reader may put it away. Off is for a notice that has to keep
	// saying itself — a maintenance window, a policy nobody may miss.
	HomeNoticeDismissible = "home.notice_dismissible"
	RegistrationEnabled   = "registration.enabled"
	RegistrationGroup     = "registration.default_group"
	RequireEmail          = "registration.require_email"
	QQRequirement         = "registration.qq_requirement"
	VerifyEmail           = "registration.verify_email"
	EmailDomains          = "registration.email_domains"
	SignupsPerMinute      = "registration.per_minute"
	SignupsPerHour        = "registration.per_hour"
	// Per address, unlike the two above, which are one counter for the whole
	// instance: a flood from one place should not lock out everybody else.
	SignupsPerIP       = "registration.per_ip"
	SignupsIPWindowMin = "registration.per_ip_window_minutes"

	// Invite codes. The backoffice shows these as one "registration mode"
	// select (open / invite / closed), but they are two independent booleans
	// underneath: invite-only is RegistrationEnabled true and InvitesRequired
	// true, and closed is RegistrationEnabled false regardless of this one —
	// see settings.InviteMode, which is the one place that composes them.
	InvitesRequired = "invites.required"
	// Whether an account gets a personal code of its own to hand to friends,
	// separate from whether a code is required to register at all: an
	// instance can require one at the door while never asking its own
	// members to bring anyone.
	InvitesUserEnabled = "invites.user_enabled"
	// Successful invites one personal code may credit before it stops
	// counting toward its owner's reward — the code itself keeps working
	// past this, since registering through it still seats someone in its
	// group; only the reward is capped. 0 = unlimited.
	InvitesUserLimit = "invites.user_limit"
	// Reset cards granted to the inviter per qualifying invitee, and how many
	// days each is good for. 0 cards is the instance-wide off switch for the
	// reward, independent of whether personal codes exist at all.
	InvitesRewardCards    = "invites.reward_cards"
	InvitesRewardCardDays = "invites.reward_card_days"
	// How many qualifying invites earn one payout of InvitesRewardCards: 1
	// pays every time (the original behaviour), 3 pays the 3rd, 6th, 9th and
	// so on. A cadence rather than a second card count, because "how many
	// invites" and "how many cards" are different numbers an operator tunes
	// separately — a partner program wants a big number rarely, a referral
	// scheme wants a small one often.
	InvitesRewardEvery = "invites.reward_every"

	// Cloudflare Turnstile. The site key is public — it is in the page's
	// markup — and the secret is write-only: it is redacted out of every
	// response, the way a provider's API key is.
	TurnstileSiteKey   = "turnstile.site_key"
	TurnstileSecretKey = "turnstile.secret_key"
	TurnstileOnLogin   = "turnstile.on_login"
	TurnstileOnSignup  = "turnstile.on_signup"
	TurnstileOnAPIKey  = "turnstile.on_api_key"
	TurnstileOnRedeem  = "turnstile.on_redeem"
	// Feedback is the one scene an account reaches while already signed in
	// and with nothing to gain, so it is off by default: it earns its
	// challenge only on an instance that has actually been spammed.
	TurnstileOnFeedback = "turnstile.on_feedback"

	// Signing in with an account somebody already holds somewhere else. Each
	// provider is a switch and a pair of credentials from its own console;
	// the secret is write-only, the way the Turnstile one is.
	//
	// The switch is separate from the credentials on purpose: an operator
	// pasting a client id is configuring, not yet opening a second front
	// door, and a button that appeared the moment a key was saved would be a
	// button nobody had finished setting up.
	OAuthGitHubEnabled = "oauth.github_enabled"
	OAuthGitHubID      = "oauth.github_client_id"
	OAuthGitHubSecret  = "oauth.github_client_secret"
	OAuthGoogleEnabled = "oauth.google_enabled"
	OAuthGoogleID      = "oauth.google_client_id"
	OAuthGoogleSecret  = "oauth.google_client_secret"
	// Whether a provider identity nobody here knows may open an account, or
	// only sign in to one that already exists. On, because an instance that
	// has closed registration already refuses it through
	// registration.enabled, and one that has not has just been handed a
	// visitor a provider vouches for.
	OAuthAllowSignup = "oauth.allow_signup"
	// Whether an address a provider has verified may adopt the account that
	// already holds it, instead of being refused as taken. On: it is what
	// makes "sign in with Google" work for the people who registered with a
	// password months ago, and the address is proven before it is believed.
	OAuthLinkByEmail = "oauth.link_by_email"

	// Whether a reader is told which operator answered their report. On by
	// default: an answer signed by a person reads as one, and the people
	// answering are the same handful whose names are already on the
	// announcements. An instance that would rather its staff not be
	// addressed by name can turn it off, and then the name does not leave
	// the server at all.
	FeedbackShowStaffName = "feedback.show_staff_name"

	// Asking a model whether a sign-up looks like a person. The prompt is not
	// a setting: one that could be edited could be turned into "refuse
	// everybody from this domain", and this runs before an account exists,
	// where a mistake has no appeal. What an operator chooses is whether it
	// runs, which model answers, and what a refusal says.
	SignupReview              = "security.signup_review"
	SignupReviewModel         = "security.signup_review_model"
	SignupReviewMode          = "security.signup_review_mode"
	SignupReviewRefusal       = "security.signup_review_refusal"
	SignupReviewRestrictHours = "security.signup_review_restrict_hours"
	// Two-step sign-in. Who must switch it on, the name an authenticator
	// app files the entry under, and how many days a browser may skip the
	// code once somebody has typed one on it.
	TwoFactorPolicy       = "security.two_factor_policy"
	TwoFactorIssuer       = "security.two_factor_issuer"
	TwoFactorRememberDays = "security.two_factor_remember_days"
	// Asking for a code again at the backoffice's door, not just at sign-in:
	// how often (one of the BackofficeVerify modes below), and the minutes
	// that mode counts.
	TwoFactorBackofficeMode    = "security.two_factor_backoffice_mode"
	TwoFactorBackofficeMinutes = "security.two_factor_backoffice_minutes"
	// Whether a visit to the backoffice ends when the requests start coming
	// from another network, or from another browser, than the one that
	// typed the code.
	TwoFactorBackofficeNetwork = "security.two_factor_backoffice_network"
	TwoFactorBackofficeBrowser = "security.two_factor_backoffice_browser"
	// Whether signing in from a device this account has never used before
	// mails the owner a short notice. Off by default for the same reason
	// every other mail-sending switch here is: it does nothing until an
	// operator has both SMTP configured and turned it on.
	NewDeviceEmail          = "security.new_device_email"
	ChatChallengeRequests   = "security.chat_challenge_requests"
	ChatChallengeWindowSecs = "security.chat_challenge_window_seconds"
	ChatChallengeClearMins  = "security.chat_challenge_clear_minutes"
	AdminsBypassQuota       = "quota.admins_bypass"
	UsageDisplay            = "quota.usage_display"
	QuotaMaxConcurrent      = "quota.max_concurrent"
	// How many times one work-surface turn may call the model. Each round
	// is a real provider request that a tool result made necessary, so this
	// is the ceiling on what a single question can cost.
	ChatAgentMaxRounds   = "chat.agent_max_rounds"
	LandingMode          = "landing.mode"
	LandingIntro         = "landing.intro"
	TrialEnabled         = "landing.trial_enabled"
	TrialTurns           = "landing.trial_turns"
	TrialModel           = "landing.trial_model"
	DefaultSystemPrompt  = "chat.default_system_prompt"
	ConversationMaxTurns = "chat.max_turns"
	AllowArchive         = "chat.allow_archive"
	APIEnabled           = "api.enabled"
	AttachmentMaxMB      = "attachments.max_mb"
	AttachmentRetain     = "attachments.retain"
	AttachmentPurgeDays  = "attachments.purge_after_days"
	AttachmentPurgeDaily = "attachments.purge_daily_at"
	AttachmentOrphanMins = "attachments.orphan_minutes"

	// Liveness. The window is both "how far back counts as evidence" and
	// "how quiet a model has to be before the system asks it directly",
	// because those are the same question asked from two sides.
	HealthProbe        = "health.probe"
	HealthWindowMins   = "health.window_minutes"
	HealthDisableAfter = "health.disable_after"
	HealthRetainDays   = "health.retain_days"
	// A second way to disable: not "it failed three times running" but "it
	// has been failing one turn in four all afternoon". A model can be badly
	// broken without ever failing twice in a row.
	HealthDisableBelow = "health.disable_below"
	// What readers are told. Off by default: an availability figure is an
	// operator's own record of their instance, and publishing it is a choice.
	HealthShowUsers = "health.show_users"
	HealthWarnBelow = "health.warn_below"
	// When uptime was last reset by an administrator. Epoch milliseconds, or
	// zero if never reset. Everything before this moment is excluded from
	// availability figures.
	HealthResetAt = "health.reset_at"
	// Written by the janitor rather than by a form, so that a restart does
	// not lose track of whether today's purge already happened. Readable in
	// the settings response and deliberately absent from the writable set.
	AttachmentPurgeLast = "attachments.purge_last_run"

	// The leaderboard readers may open from the account menu. Off by default
	// for the reason uptime is: it tells every account what the others have
	// been doing, and that is a thing an operator decides to publish rather
	// than a thing an upgrade publishes for them.
	LeaderboardShowUsers = "leaderboard.show_users"
	// How the other people on it are named. See LeaderboardIdentities.
	LeaderboardIdentity = "leaderboard.identity"
	// How many places are shown. The reader's own place is shown whatever it
	// is, so this bounds the list, not who can find themselves on it.
	LeaderboardSize = "leaderboard.size"
	// Whether the busiest-models board is shown beside the accounts one.
	LeaderboardShowModels = "leaderboard.show_models"

	// The browser tab and the PWA install card. Both fall back to the site's
	// own name (and, for the description, site.description) rather than
	// needing a second copy typed in — an operator who never opens this card
	// still gets a tab and an install prompt that say who they are, not
	// "Obsidian Arc" on every instance at once.
	SiteBrowserTitle = "site.browser_title"
	PWAName          = "pwa.name"
	PWAShortName     = "pwa.short_name"
	PWADescription   = "pwa.description"
	// #rrggbb only — see ValidHexColor. Kept as two settings rather than one
	// because a manifest itself distinguishes the browser chrome's tint from
	// the splash screen shown behind the icon while the app loads.
	PWAThemeColor      = "pwa.theme_color"
	PWABackgroundColor = "pwa.background_color"
	// Empty means the built-in mark (the same triangle the favicon already
	// draws). Validated like an avatar — see ValidPWAIconURL — because a
	// root-relative path or a data URI are the only references the image
	// policy (`img-src 'self' data: blob:`) actually allows through.
	PWAIconURL = "pwa.icon_url"
)

// MaxAttachmentCeilingMB bounds what an operator may set. A per-file limit
// larger than this is not a policy, it is a way to run out of memory: an
// upload is read into a buffer before it is stored.
const MaxAttachmentCeilingMB = 64

// How the usage figures are phrased for a user. An operator who has set
// generous limits usually wants a reassuring "80% left"; one running a tight
// instance wants "20% used" or the raw numbers. It changes only the wording —
// what is enforced is the same either way.
const (
	UsageAbsolute  = "absolute"
	UsageRemaining = "remaining"
	UsageUsed      = "used"
)

// What a visitor with no account is shown at the front door.
const (
	// Straight to the sign-in card. What the instance did before there
	// was a choice, and still the default.
	LandingLogin = "login"
	// A page the operator writes, with a way in from it.
	LandingIntroPage = "intro"
	// The chat itself, read-only unless a trial is enabled.
	LandingChat = "chat"
	// The product's own front page — written here rather than by the
	// operator, so an instance gets one without anybody composing HTML.
	// It says what the software is; `intro` is still how an operator says
	// what *their* instance is.
	LandingSite = "site"
)

var LandingModes = []string{LandingLogin, LandingIntroPage, LandingChat, LandingSite}

func ValidLandingMode(value string) bool {
	for _, candidate := range LandingModes {
		if value == candidate {
			return true
		}
	}
	return false
}

// The ceiling on a trial, enforced here rather than trusted from the
// form: every trial turn is spent from the operator's own credit by
// someone who has not identified themselves.
const MaxTrialTurns = 20

// UsageDisplays is the set the admin form offers and the only set the server
// accepts, so a typo cannot leave every user looking at a blank figure.
var UsageDisplays = []string{UsageAbsolute, UsageRemaining, UsageUsed}

func ValidUsageDisplay(value string) bool {
	for _, candidate := range UsageDisplays {
		if value == candidate {
			return true
		}
	}
	return false
}

// How the leaderboard names the accounts on it, other than the reader's own.
const (
	// The name and picture each person chose for themselves, which is what
	// they already show everyone they talk to here.
	LeaderboardNickname = "nickname"
	// Nobody but the reader: everyone else is a place number. For an
	// instance whose users would rather not be seen to spend.
	LeaderboardAnonymous = "anonymous"
	// The nickname with the handle under it, as the backoffice shows them.
	// Nicknames are not unique; handles are.
	LeaderboardHandle = "handle"
)

var LeaderboardIdentities = []string{LeaderboardNickname, LeaderboardAnonymous, LeaderboardHandle}

func ValidLeaderboardIdentity(value string) bool {
	for _, candidate := range LeaderboardIdentities {
		if value == candidate {
			return true
		}
	}
	return false
}

// MaxLeaderboardSize bounds the list. Every place shown costs a lookup for its
// picture, and past a hundred it is a table rather than a leaderboard.
const MaxLeaderboardSize = 100

// Who has to have two-step sign-in switched on. Each level includes the
// one before it: an administrator who must enrol before signing in has
// certainly enrolled before opening the backoffice.
const (
	// Anybody may switch it on; nobody has to.
	TwoFactorOptional = "optional"
	// Administrators may sign in and chat without it, but the backoffice
	// refuses them until they have it — the pages that can change who
	// everybody else is are the ones worth a second lock.
	TwoFactorBackoffice = "backoffice"
	// Administrators must enrol before the product will do anything else.
	TwoFactorAdmins = "admins"
	// Every account must enrol before the product will do anything else.
	TwoFactorEveryone = "everyone"
)

var TwoFactorPolicies = []string{TwoFactorOptional, TwoFactorBackoffice, TwoFactorAdmins, TwoFactorEveryone}

func ValidTwoFactorPolicy(value string) bool {
	for _, candidate := range TwoFactorPolicies {
		if value == candidate {
			return true
		}
	}
	return false
}

// MaxTwoFactorRememberDays bounds "don't ask on this browser". A year is
// already longer than most people keep a browser profile.
const MaxTwoFactorRememberDays = 365

// How often the backoffice asks for a code again, on top of the one signing
// in took. Every mode but off makes the minutes mean something different,
// because "how long" is a different question for each.
const (
	BackofficeVerifyOff = "off"
	// Every visit: leaving the backoffice — back to the chat, a reload, a
	// closed tab — ends it, and so do the minutes without a request, for
	// the tab that was simply left open.
	BackofficeVerifyVisit = "visit"
	// After idling: working in the backoffice keeps it open, coming and
	// going does not close it, and the minutes without a request do.
	BackofficeVerifyIdle = "idle"
	// On a schedule: one code is good for the minutes after it was typed,
	// whatever happens in them, and then another is asked for.
	BackofficeVerifyInterval = "interval"
)

var BackofficeVerifyModes = []string{BackofficeVerifyOff, BackofficeVerifyVisit, BackofficeVerifyIdle, BackofficeVerifyInterval}

func ValidBackofficeVerifyMode(value string) bool {
	for _, candidate := range BackofficeVerifyModes {
		if value == candidate {
			return true
		}
	}
	return false
}

// MaxTwoFactorBackofficeMinutes bounds the minutes: a week, which is already
// "once in a while" rather than a lock anybody would notice.
const MaxTwoFactorBackofficeMinutes = 7 * 24 * 60

// What new accounts are required to provide regarding QQ numbers.
const (
	QQDisabled = "off"
	QQOptional = "optional"
	QQRequired = "required"
)

var QQRequirements = []string{QQDisabled, QQOptional, QQRequired}

func ValidQQRequirement(value string) bool {
	for _, candidate := range QQRequirements {
		if value == candidate {
			return true
		}
	}
	return false
}

// What the backoffice's one registration-mode select actually means, and
// what GET /api/site tells a visitor before they type anything.
const (
	InviteModeOpen   = "open"
	InviteModeInvite = "invite"
	InviteModeClosed = "closed"
)

// InviteMode composes RegistrationEnabled and InvitesRequired into the one
// value the form and the sign-up page both read, so neither has to reproduce
// the rule that closed wins regardless of whether a code is also required.
func InviteMode(registrationEnabled, invitesRequired bool) string {
	if !registrationEnabled {
		return InviteModeClosed
	}
	if invitesRequired {
		return InviteModeInvite
	}
	return InviteModeOpen
}

// hexColorRE accepts exactly #rrggbb. Three-digit and named CSS colours are
// legal everywhere else, but a manifest generator that has to guess which of
// those an operator meant is a second source of truth for the same value —
// one form removes the guess.
var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidHexColor reports whether value is a manifest-ready colour. An empty
// string is not valid on its own terms — callers that treat "no override" as
// acceptable check for it themselves, the way updateSettings does.
func ValidHexColor(value string) bool {
	return hexColorRE.MatchString(value)
}

// pwaIconRE mirrors web/src/lib/account.ts's safeAvatar: a data URI is the
// other reference the image policy (`img-src 'self' data: blob:') allows
// besides a path on this server, so the two checks have to agree or one of
// them is lying to an operator about what will actually render.
var pwaIconRE = regexp.MustCompile(`^data:image/(?:png|jpeg|webp|gif|avif|svg\+xml);base64,[A-Za-z0-9+/]+=*$`)

// ValidPWAIconURL reports whether value is empty (the built-in icon), a
// root-relative path, or an inline data URI. An absolute URL to another host
// is refused here rather than left for the browser to silently drop: the
// manifest's own Content-Security-Policy has no reason to trust an arbitrary
// origin with the icon a user is asked to install.
func ValidPWAIconURL(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return true
	}
	return pwaIconRE.MatchString(value)
}

// Defaults are what a fresh instance behaves like, and what a deleted row
// falls back to. Nothing reads a setting without one.
var Defaults = map[string]string{
	SiteName:        "Obsidian Arc",
	SiteDescription: "",
	AboutTitle:      "",
	AboutBody:       "",
	HomeNotice:      "",
	// Dismissible unless an operator says otherwise: a strip that cannot be
	// put away is the exception, and defaults should not be the exception.
	HomeNoticeDismissible: "true",
	RegistrationEnabled:   "true",
	RegistrationGroup:     "",
	RequireEmail:          "false",
	QQRequirement:         QQDisabled,
	// Inert without SMTP, whatever it says: see auth.VerificationRequired.
	VerifyEmail:  "false",
	EmailDomains: "",
	// Zero means unthrottled. An instance that has closed
	// registration needs neither, so neither is on by default.
	SignupsPerMinute: "0",
	SignupsPerHour:   "0",
	// Off until an operator sets it. A limit guessed on their behalf is a
	// limit that locks out a university or an office behind one address.
	SignupsPerIP:       "0",
	SignupsIPWindowMin: "60",
	// Off: an instance that has never configured an invite scheme should
	// register exactly as it always did, not suddenly demand a code nobody
	// has issued.
	InvitesRequired:       "false",
	InvitesUserEnabled:    "false",
	InvitesUserLimit:      "10",
	InvitesRewardCards:    "0",
	InvitesRewardCardDays: "30",
	InvitesRewardEvery:    "1",
	TurnstileSiteKey:      "",
	TurnstileSecretKey:    "",
	// Off, and off even once the keys are filled in: an operator pasting keys
	// is configuring, not yet switching on, and a challenge that appeared the
	// moment a key was saved would lock out the half-finished setup it was
	// saved during.
	TurnstileOnLogin:      "false",
	TurnstileOnSignup:     "false",
	TurnstileOnAPIKey:     "false",
	TurnstileOnRedeem:     "false",
	TurnstileOnFeedback:   "false",
	FeedbackShowStaffName: "true",
	// Off, and off even once the credentials are filled in, for the reason
	// the challenge switches above are: pasting a key is not the same as
	// opening the door.
	OAuthGitHubEnabled: "false",
	OAuthGitHubID:      "",
	OAuthGitHubSecret:  "",
	OAuthGoogleEnabled: "false",
	OAuthGoogleID:      "",
	OAuthGoogleSecret:  "",
	OAuthAllowSignup:   "true",
	OAuthLinkByEmail:   "true",
	SignupReview:       "false",
	SignupReviewModel:  "",
	// Loose, normal or strict. Normal refuses what reads as generated and
	// allows what reads as chosen; the other two move the line, and strict
	// also refuses when the model cannot answer at all.
	SignupReviewMode: "normal",
	// What a refused person reads. Empty means the sentence built into the
	// client, which says only that the sign-up was not accepted — an operator
	// who wants to offer a way to appeal writes it here.
	SignupReviewRefusal:       "",
	SignupReviewRestrictHours: "24",
	// Optional: a policy that suddenly asked every account for a code would
	// lock out everybody who has never heard of an authenticator app, the
	// moment the software was upgraded.
	TwoFactorPolicy: TwoFactorOptional,
	// Empty means the site's own name, which is what an operator who never
	// opens this screen would have typed anyway.
	TwoFactorIssuer: "",
	// Off. Remembering a browser trades the second factor for a cookie, and
	// that is a trade an operator should make on purpose.
	TwoFactorRememberDays: "0",
	// Off: a code at the backoffice's door is a real cost to the people who
	// use it all day, and one an operator should choose to pay.
	TwoFactorBackofficeMode: BackofficeVerifyOff,
	// Long enough for one sitting's work, short enough that a tab left open
	// over lunch locks itself.
	TwoFactorBackofficeMinutes: "15",
	// Off: phones change address all day, and an operator should choose to
	// be asked again every time they do.
	TwoFactorBackofficeNetwork: "false",
	TwoFactorBackofficeBrowser: "false",
	// Off until an operator turns it on, whatever mail is configured: a
	// switch that mailed everyone the moment SMTP was set up would be a
	// surprise, not a feature anyone asked for.
	NewDeviceEmail: "false",
	// Zero leaves the mid-chat challenge off. Once enabled, the other two
	// defaults describe a short burst and a clearance long enough that a real
	// reader is not challenged again during the same conversation.
	ChatChallengeRequests:   "0",
	ChatChallengeWindowSecs: "60",
	ChatChallengeClearMins:  "30",
	AdminsBypassQuota:       "true",
	UsageDisplay:            UsageAbsolute,
	QuotaMaxConcurrent:      "4",
	ChatAgentMaxRounds:      "8",
	LandingMode:             LandingLogin,
	LandingIntro:            "",
	TrialEnabled:            "false",
	TrialTurns:              "3",
	TrialModel:              "",
	DefaultSystemPrompt:     "",
	ConversationMaxTurns:    "40",
	AllowArchive:            "true",
	// On: asking a model nobody has used costs one token and answers the
	// question the liveness column exists for. Off, a quiet model reads as
	// "no data" forever, which is the state this feature was built to end.
	HealthProbe:      "true",
	HealthWindowMins: "30",
	// Off. Turning a model off on the system's own judgement is a decision an
	// operator has to make deliberately — the failure mode of guessing is an
	// instance that quietly stops offering the model everyone uses.
	HealthDisableAfter: "0",
	HealthRetainDays:   "14",
	HealthDisableBelow: "0",
	HealthShowUsers:    "false",
	// A model failing one turn in ten is worth warning about before somebody
	// types a long question into it. Off would be the safer default and a
	// worse one: nobody switches on a warning they have not been bitten by.
	HealthWarnBelow: "90",
	HealthResetAt:   "0",
	// Off until an operator says otherwise: it opens a second way to spend
	// the instance's provider credit, one that no longer goes through a
	// browser session.
	APIEnabled:      "false",
	AttachmentMaxMB: "6",
	// Off, so an image reaches the provider and is then dropped. Turning it
	// on makes this server the durable home of every picture anyone sends,
	// which buys one thing: a model that can still see an image several
	// turns after it was sent.
	AttachmentRetain: "false",
	// Zero and empty mean "no scheduled cleanup". The default policy already
	// drops an image as soon as its turn is sent, so a fresh instance has
	// nothing for these to do.
	AttachmentPurgeDays:  "0",
	AttachmentPurgeDaily: "",
	// An upload that was never sent. Short, because it is the one window in
	// which this server holds a picture it has no use for.
	AttachmentOrphanMins: "60",
	AttachmentPurgeLast:  "0",

	LeaderboardShowUsers:  "false",
	LeaderboardIdentity:   LeaderboardNickname,
	LeaderboardSize:       "20",
	LeaderboardShowModels: "true",

	SiteBrowserTitle: "",
	PWAName:          "",
	PWAShortName:     "",
	PWADescription:   "",
	// The interface's own default accent (web/src/theme/color-utils.ts:
	// ACCENTS.neutral), so a fresh instance's install prompt and splash
	// screen are already themed instead of showing the browser's white.
	PWAThemeColor:      "#18181b",
	PWABackgroundColor: "#18181b",
	PWAIconURL:         "",
}

type Service struct {
	db *database.DB

	mu               sync.RWMutex
	values           map[string]string
	loginBackgrounds map[string]int64
	siteLogoAt       int64
}

func New(db *database.DB) *Service {
	return &Service{db: db, values: map[string]string{}, loginBackgrounds: map[string]int64{}, siteLogoAt: 0}
}

// Load reads the table into memory. Called once at boot; after that the cache
// is kept current by Set.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return fmt.Errorf("settings: load: %w", err)
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("settings: scan: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("settings: load: %w", err)
	}

	bgRows, err := s.db.Query(ctx, `SELECT variant, updated_at FROM login_backgrounds`)
	bgs := map[string]int64{}
	if err == nil {
		defer bgRows.Close()
		for bgRows.Next() {
			var v string
			var at int64
			if err := bgRows.Scan(&v, &at); err == nil {
				bgs[v] = at
			}
		}
	}

	var logoAt int64
	logoErr := s.db.QueryRow(ctx, `SELECT updated_at FROM site_logo WHERE id = 'default'`).Scan(&logoAt)
	if logoErr != nil && !database.IsNotFound(logoErr) {
		// table may not exist in minimal tests or is unmigrated; ignore not found
		logoAt = 0
	}

	s.mu.Lock()
	s.values = values
	s.loginBackgrounds = bgs
	s.siteLogoAt = logoAt
	s.mu.Unlock()
	return nil
}

func (s *Service) Get(key string) string {
	s.mu.RLock()
	value, ok := s.values[key]
	s.mu.RUnlock()
	if ok {
		return value
	}
	return Defaults[key]
}

func (s *Service) Bool(key string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(s.Get(key)))
	if err != nil {
		return false
	}
	return value
}

func (s *Service) Int(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(s.Get(key)))
	if err != nil {
		return fallback
	}
	return value
}

// BrowserTitle is the effective browser tab title: the operator's own text if
// they set one, the site's own name otherwise. Both the server's own
// index.html and the /api/site response ask here, so the fallback is decided
// once rather than repeated at every place that would otherwise have to know
// site.browser_title exists.
func (s *Service) BrowserTitle() string {
	if title := s.Get(SiteBrowserTitle); title != "" {
		return title
	}
	return s.Get(SiteName)
}

// All returns every known key with its effective value, so the admin screen
// can render settings that have never been written.
func (s *Service) All() map[string]string {
	out := make(map[string]string, len(Defaults))
	for key, value := range Defaults {
		out[key] = value
	}
	s.mu.RLock()
	for key, value := range s.values {
		out[key] = value
	}
	s.mu.RUnlock()
	return out
}

func (s *Service) Set(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("settings: set %s: %w", key, err)
	}
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

func (s *Service) SetMany(ctx context.Context, values map[string]string) error {
	now := time.Now().UnixMilli()
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		for key, value := range values {
			if _, err := tx.Exec(ctx,
				`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
				key, value, now); err != nil {
				return fmt.Errorf("settings: set %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	for key, value := range values {
		s.values[key] = value
	}
	s.mu.Unlock()
	return nil
}
