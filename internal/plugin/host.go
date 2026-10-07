package plugin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Host is the server being assembled, as a plugin's Setup sees it: the core
// modules it may attach to, already constructed and wired to each other.
//
// Each field is a module's own type, so what a plugin can do with it is what
// that module exports — the extension points are methods on the modules
// (auth.Service.AddGuard, user.DefineField, admin.Handlers.Mount, …), not a
// second API invented here. Each records the plugin that attached to it —
// the Plugin field on what is registered — and asks the gate before it runs
// it; Handle and AllowOrigin are the two the host keeps itself.
type Host struct {
	DB       *database.DB
	Settings *settings.Service
	Users    *user.Store
	Security *securityevents.Store
	Notify   *notify.Store
	Cards    *card.Store
	Bonus    *bonus.Store

	Auth          *auth.Service
	AuthHandlers  *auth.Handlers
	OAuth         *oauth.Service
	OAuthHandlers *oauth.Handlers

	Invites        *invite.Store
	InviteHandlers *invite.Handlers

	Admin *admin.Handlers

	// The public mux. A plugin mounts on it through Handle, which is what
	// takes the route away while the plugin is switched off.
	Mux *http.ServeMux
	// The caller's address as the proxy settings resolve it.
	ClientIP func(*http.Request) string
	// The configured public URL, for a plugin that has to build an absolute
	// address to itself. Empty when none is configured.
	PublicURL func() string
	// The instance's own human check, for a plugin that puts it in front of
	// something of its own. Zero means none is wired, and asking for one is
	// then refused rather than passed.
	Challenge Challenge

	// Which plugins are switched on. Every extension point above holds the
	// same gate; Host asks it for the two it hands out itself.
	Gate plugingate.Gate

	// The origins of the packages installed on this server, asked for on every
	// response like the compiled-in plugins' own. Set by the manager.
	dynamicOrigins func() []string

	// The plugin this copy of the host was handed to (see SetupAll).
	name string
	// What every plugin's copy writes to. Written by Setup, read by every
	// response afterwards; setup finishes before the first request, so
	// there is nothing to lock.
	shared *hostShared
}

// Challenge is the human check a plugin may borrow: which kinds the
// instance would ask for right now, and the check of what the browser
// solved. Functions, because both follow settings an operator changes while
// the process runs.
type Challenge struct {
	Describe func() ChallengeKinds
	// Verify checks the proof against what Describe asks for; ip is the
	// address the proof came from, which Turnstile weighs.
	Verify func(ctx context.Context, proof ChallengeProof, ip string) error
}

// ChallengeKinds is what a browser must solve: proof of work, a Turnstile
// widget drawn with this key, or both. Both empty means nothing is asked.
type ChallengeKinds struct {
	PoW              bool   `json:"pow"`
	TurnstileSiteKey string `json:"turnstile_site_key"`
}

// ChallengeProof is what the browser solved, as it sent it.
type ChallengeProof struct {
	Turnstile string          `json:"turnstile"`
	PoW       json.RawMessage `json:"pow"`
}

type hostShared struct {
	origins []origin
	routes  []PublicRoute
}

type origin struct {
	plugin string
	fn     func() string
}

// PublicRoute is an endpoint a plugin put on the public mux, for its
// manifest.
type PublicRoute struct {
	Plugin  string `json:"-"`
	Pattern string `json:"pattern"`
}

// share makes the lists every copy writes to. SetupAll does it before it
// copies; this is for a host built by hand, as a test builds one.
func (h *Host) share() {
	if h.shared == nil {
		h.shared = &hostShared{}
	}
}

// Name is the plugin this host was handed to.
func (h *Host) Name() string { return h.name }

// Active reports whether the plugin is switched on right now, for the rare
// check no extension point makes for it — a background loop of its own.
func (h *Host) Active() bool { return h.Gate.Allows(h.name) }

// Handle mounts a public endpoint for the plugin: behind the same middleware
// as every other route, and answering 404 while the plugin is switched off.
// A route that must not be reached anonymously checks for itself, the way
// the core's do.
func (h *Host) Handle(pattern string, handler http.Handler) {
	gate, name := h.Gate, h.name
	h.share()
	h.shared.routes = append(h.shared.routes, PublicRoute{Plugin: name, Pattern: pattern})
	h.Mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gate.Allows(name) {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

// AllowOrigin widens the page's Content-Security-Policy by one origin, for a
// plugin that loads a service's script into the page. fn is asked on every
// response and answers the scheme and host while the service is in use and
// "" otherwise — so switching the service off takes the exception away with
// it, the rule the Turnstile widening keeps. Switching the plugin off does
// the same.
func (h *Host) AllowOrigin(fn func() string) {
	h.share()
	h.shared.origins = append(h.shared.origins, origin{plugin: h.name, fn: fn})
}

// Origins is every origin the enabled plugins currently ask for.
func (h *Host) Origins() []string {
	var out []string
	if h.dynamicOrigins != nil {
		out = append(out, h.dynamicOrigins()...)
	}
	if h.shared == nil {
		return out
	}
	for _, o := range h.shared.origins {
		if !h.Gate.Allows(o.plugin) {
			continue
		}
		if value := o.fn(); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (h *Host) publicRoutes(plugin string) []PublicRoute {
	if h.shared == nil {
		return nil
	}
	var out []PublicRoute
	for _, route := range h.shared.routes {
		if route.Plugin == plugin {
			out = append(out, route)
		}
	}
	return out
}
