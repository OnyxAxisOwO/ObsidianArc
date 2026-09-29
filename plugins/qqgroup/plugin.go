// Package qqgroup is an instance built around a QQ group: members carry
// their QQ number on the account, a community sign-in that vouches for the
// number binds it, and a member leaving the group ends their account and
// takes back what their invitation earned.
//
// None of that is how an instance in general works, which is why it is a
// plugin. What it adds to the core is exactly this:
//
//   - a users.qq column, unique and searchable (user.DefineField), with a
//     setting for whether sign-up asks for it (auth.Service.SetFieldRule);
//   - the OpenID Connect subject bound to that column when it is all digits
//     (oauth.Service.BindSubject), which is the community sign-in's shape;
//   - group departures: the backoffice action, the bot's webhook, and the
//     tombstones both write, badged in each inviter's own list;
//   - the console commands for the two backoffice routes.
//
// The settings, the column and the table keep the names they had when this
// was part of the core, and the two migrations keep their versions, so an
// instance that ran them as core carries on by compiling the plugin in.
package qqgroup

import (
	"embed"
	"errors"
	"io/fs"
	"regexp"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Name is the plugin's identifier: the build tag is plugin_qqgroup, and the
// browser's half lives in web/src/plugins/qqgroup.
const Name = "qqgroup"

// Field is the account column, and the key under User.Fields.
const Field = "qq"

const (
	// What new accounts are required to provide: auth.FieldOff,
	// FieldOptional or FieldRequired.
	Requirement = "registration.qq_requirement"
	// The bot's webhook. The token is what authenticates POST
	// /api/bot/departure — the one externally reachable way to process a
	// departure — and empty means the endpoint answers nothing at all, which
	// is the default: an instance with no bot has no reason to expose it.
	BotWebhookToken = "bot.webhook_token"
	// What the bot's call does when the event itself does not say.
	BotDepartureMode = "bot.departure_mode"
)

// What processing a departure does to the departing account. Shared by the
// backoffice's action and the bot's webhook so neither can invent a third:
// disable keeps the account (and, with it, the QQ number a fresh
// registration would otherwise claim immediately), delete removes it and
// everything cascading from it.
const (
	ModeDisable = "disable"
	ModeDelete  = "delete"
)

func validMode(value string) bool { return value == ModeDisable || value == ModeDelete }

var qqRE = regexp.MustCompile(`^[1-9][0-9]{4,14}$`)

// ValidQQ reports whether value is a QQ number's shape: five to fifteen
// digits, no leading zero.
func ValidQQ(value string) bool { return qqRE.MatchString(value) }

//go:embed migrations/*.sql
var migrations embed.FS

func init() {
	user.DefineField(user.Field{
		Key: Field, Unique: true, Searchable: true,
		Validate: func(v string) error {
			if !ValidQQ(v) {
				return errors.New("a QQ number is five to fifteen digits")
			}
			return nil
		},
	})
	settings.Define(settings.Definition{
		Key: Requirement, Default: auth.FieldOff,
		Validate: func(v string) error {
			switch v {
			case auth.FieldOff, auth.FieldOptional, auth.FieldRequired:
				return nil
			}
			return errors.New("must be off, optional or required")
		},
	})
	// Write-only, like the other credentials, and on the security screen
	// with them: the bot can disable or delete accounts, which is a
	// front-door concern.
	settings.Define(settings.Definition{Key: BotWebhookToken, Secret: true, Permission: "security"})
	// The reversible one by default: a bot misfire should cost an
	// administrator a re-enable, not an account. A mode nothing reads would
	// silently do the wrong thing to every account the bot reports, so it is
	// refused rather than defaulted.
	settings.Define(settings.Definition{
		Key: BotDepartureMode, Default: ModeDisable, Permission: "security",
		Validate: func(v string) error {
			if !validMode(v) {
				return errors.New("must be disable or delete")
			}
			return nil
		},
	})
	plugin.Register(qqPlugin{})
}

type qqPlugin struct{}

func (qqPlugin) Name() string { return Name }

func (qqPlugin) Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

func (qqPlugin) Setup(h *plugin.Host) error {
	set := h.Settings
	h.Auth.SetFieldRule(Field, func() string { return set.Get(Requirement) })

	// The community sign-in verifies the number by a group message before it
	// vouches, and its subject is the number itself. Anything that is not
	// digits is some other identity provider and is left alone.
	h.OAuth.BindSubject(oauthBinding())

	departures := NewDepartures(h.DB, h.Users, h.Cards, set)
	departures.Notify = h.Notify
	departures.Security = h.Security
	departures.Sessions = h.Auth.Sessions()

	admin := &adminHandlers{departures: departures, clientIP: h.ClientIP}
	admin.mount(h.Admin)

	bot := newBotHandlers(departures, set)
	bot.ClientIP = h.ClientIP
	bot.Routes(h.Mux)

	h.InviteHandlers.DecorateInvitees(departures.decorateInvitees)
	return nil
}
