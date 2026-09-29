package plugin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Host is the server being assembled, as a plugin's Setup sees it: the core
// modules it may attach to, already constructed and wired to each other.
//
// Each field is a module's own type, so what a plugin can do with it is what
// that module exports — the extension points are methods on the modules
// (auth.Service.AddGuard, user.Store.AddField, admin.Handlers.Mount, …),
// not a second API invented here.
type Host struct {
	DB       *database.DB
	Settings *settings.Service
	Users    *user.Store
	Security *securityevents.Store
	Notify   *notify.Store
	Cards    *card.Store

	Auth          *auth.Service
	AuthHandlers  *auth.Handlers
	OAuth         *oauth.Service
	OAuthHandlers *oauth.Handlers

	Invites        *invite.Store
	InviteHandlers *invite.Handlers

	Admin *admin.Handlers

	// The public mux, for an endpoint that sits outside /api/admin. It is
	// behind the same middleware as every other route; a route that must
	// not be reached anonymously checks for itself, the way the core's do.
	Mux *http.ServeMux
	// The caller's address as the proxy settings resolve it.
	ClientIP func(*http.Request) string
	// The configured public URL, for a plugin that has to build an absolute
	// address to itself. Empty when none is configured.
	PublicURL func() string

	// Written by Setup, read by every response afterwards; setup finishes
	// before the first request, so there is nothing to lock.
	origins []func() string
}

// AllowOrigin widens the page's Content-Security-Policy by one origin, for a
// plugin that loads a service's script into the page. fn is asked on every
// response and answers the scheme and host while the service is in use and
// "" otherwise — so switching the service off takes the exception away with
// it, the rule the Turnstile widening keeps.
func (h *Host) AllowOrigin(fn func() string) {
	h.origins = append(h.origins, fn)
}

// Origins is every origin the plugins currently ask for.
func (h *Host) Origins() []string {
	if len(h.origins) == 0 {
		return nil
	}
	out := make([]string, 0, len(h.origins))
	for _, fn := range h.origins {
		if origin := fn(); origin != "" {
			out = append(out, origin)
		}
	}
	return out
}
