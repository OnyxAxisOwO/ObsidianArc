package plugin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// The backoffice grants for this screen, one per kind of change, so a super
// administrator can let somebody look without letting them switch, and
// switch without letting them remove. Reading is any of the three: a grant
// to change something includes seeing what it is changing.
const (
	PermissionView   = "plugins"
	PermissionManage = "plugins_manage"
	PermissionRemove = "plugins_remove"
)

// Handlers is the plugins screen's API.
type Handlers struct {
	manager  *Manager
	auth     *auth.Service
	clientIP func(*http.Request) string
}

func NewHandlers(manager *Manager, authService *auth.Service, clientIP func(*http.Request) string) *Handlers {
	return &Handlers{manager: manager, auth: authService, clientIP: clientIP}
}

// Mount puts the routes in the backoffice's table, behind its wrapper — the
// same session, two-step and grant checks every other page's routes pass.
func (h *Handlers) Mount(backoffice *admin.Handlers) {
	view := PermissionView + "," + PermissionManage + "," + PermissionRemove
	for _, route := range []admin.Route{
		{Pattern: "GET /api/admin/plugins", Permission: view, Handler: h.list},
		{Pattern: "GET /api/admin/plugins/{name}", Permission: view, Handler: h.show},
		{Pattern: "POST /api/admin/plugins/{name}/install", Permission: PermissionManage, Handler: h.install},
		{Pattern: "POST /api/admin/plugins/{name}/enable", Permission: PermissionManage, Handler: h.enable},
		{Pattern: "POST /api/admin/plugins/{name}/disable", Permission: PermissionManage, Handler: h.disable},
		{Pattern: "POST /api/admin/plugins/{name}/uninstall", Permission: PermissionRemove, Handler: h.uninstall},
	} {
		backoffice.Mount(route)
	}
}

func (h *Handlers) list(w http.ResponseWriter, _ *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"plugins": h.manager.List()})
}

func (h *Handlers) show(w http.ResponseWriter, r *http.Request) error {
	info, err := h.manager.Info(r.PathValue("name"))
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"plugin": info})
}

type installBody struct {
	Enable   bool              `json:"enable"`
	Settings map[string]string `json:"settings"`
}

func (h *Handlers) install(w http.ResponseWriter, r *http.Request) error {
	var body installBody
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}
	name := r.PathValue("name")
	if err := h.manager.Install(r.Context(), h.actor(r), name, InstallOptions{
		Enable: body.Enable, Settings: body.Settings,
	}); err != nil {
		return translate(err)
	}
	return h.answer(w, name)
}

func (h *Handlers) enable(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if err := h.manager.Enable(r.Context(), h.actor(r), name); err != nil {
		return translate(err)
	}
	return h.answer(w, name)
}

type confirmBody struct {
	TwoFactorCode string `json:"two_factor_code"`
	Purge         bool   `json:"purge"`
}

// Switching a plugin off and taking it away both need a code from the
// actor's authenticator, and an account without one cannot do either: a
// plugin can be the guard in front of sign-up, and a stolen session
// switching it off is a front door left open with nothing on screen to
// show for it. Switching one on is not held to that — it adds a check
// rather than removing one.
func (h *Handlers) disable(w http.ResponseWriter, r *http.Request) error {
	var body confirmBody
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	name := r.PathValue("name")
	if _, err := h.manager.Info(name); err != nil {
		return translate(err)
	}
	if err := h.verify(w, r, body.TwoFactorCode); err != nil {
		return err
	}
	if err := h.manager.Disable(r.Context(), h.actor(r), name); err != nil {
		return translate(err)
	}
	return h.answer(w, name)
}

func (h *Handlers) uninstall(w http.ResponseWriter, r *http.Request) error {
	var body confirmBody
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	name := r.PathValue("name")
	if _, err := h.manager.Info(name); err != nil {
		return translate(err)
	}
	if err := h.verify(w, r, body.TwoFactorCode); err != nil {
		return err
	}
	if err := h.manager.Uninstall(r.Context(), h.actor(r), name, body.Purge); err != nil {
		return translate(err)
	}
	return h.answer(w, name)
}

func (h *Handlers) verify(w http.ResponseWriter, r *http.Request, code string) error {
	actor := auth.MustUser(r.Context())
	if !actor.TwoFactorEnabled() {
		return httpx.ForbiddenCode("two_factor_required",
			"Switching off or removing a plugin requires two-step verification enabled on your account.")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return httpx.BadRequestCode("two_factor_code_required",
			"Enter a code from your authenticator app to switch off or remove a plugin.")
	}
	if err := h.auth.VerifyTwoFactorCode(r.Context(), actor, code, h.ip(r)); err != nil {
		return auth.TranslateTwoFactorError(w, err)
	}
	return nil
}

// answer is the plugin as it now stands and the whole list, so the screen
// redraws from one response.
func (h *Handlers) answer(w http.ResponseWriter, name string) error {
	info, err := h.manager.Info(name)
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"plugin": info, "plugins": h.manager.List()})
}

func (h *Handlers) actor(r *http.Request) Actor {
	account := auth.MustUser(r.Context())
	return Actor{ID: account.ID, Username: account.Username, IP: h.ip(r)}
}

func (h *Handlers) ip(r *http.Request) string {
	if h.clientIP == nil {
		return ""
	}
	return h.clientIP(r)
}

func translate(err error) error {
	var setting *SettingError
	switch {
	case errors.As(err, &setting):
		return httpx.BadRequestCode("plugin_setting_invalid", "Setting %q: %s", setting.Key, setting.Reason)
	case errors.Is(err, ErrUnknown):
		return httpx.NotFound("No such plugin in this build.")
	case errors.Is(err, ErrInstalled):
		return httpx.Conflict("plugin_installed", "That plugin is already installed.")
	case errors.Is(err, ErrNotInstalled):
		return httpx.Conflict("plugin_not_installed", "That plugin is not installed.")
	case errors.Is(err, ErrAlreadyInState):
		return httpx.Conflict("plugin_unchanged", "That plugin is already in that state.")
	}
	return httpx.Internal(err)
}
