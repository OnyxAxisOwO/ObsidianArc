package plugin

import (
	"archive/zip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
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
	settings *settings.Service
	clientIP func(*http.Request) string
}

func NewHandlers(manager *Manager, authService *auth.Service, settingsService *settings.Service, clientIP func(*http.Request) string) *Handlers {
	return &Handlers{manager: manager, auth: authService, settings: settingsService, clientIP: clientIP}
}

func (h *Handlers) twoFactorRequired() bool {
	if h.settings == nil {
		return false
	}
	return h.settings.Bool(settings.TwoFactorPluginManage)
}

// Mount puts the routes in the backoffice's table, behind its wrapper — the
// same session, two-step and grant checks every other page's routes pass.
func (h *Handlers) Mount(backoffice *admin.Handlers) {
	view := PermissionView + "," + PermissionManage + "," + PermissionRemove
	for _, route := range []admin.Route{
		{Pattern: "GET /api/admin/plugins", Permission: view, Handler: h.list},
		{Pattern: "GET /api/admin/plugins/{name}", Permission: view, Handler: h.show},
		{Pattern: "POST /api/admin/plugins/upload", Permission: PermissionManage, Handler: h.upload},
		{Pattern: "POST /api/admin/plugins/{name}/install", Permission: PermissionManage, Handler: h.install},
		{Pattern: "POST /api/admin/plugins/{name}/enable", Permission: PermissionManage, Handler: h.enable},
		{Pattern: "POST /api/admin/plugins/{name}/disable", Permission: PermissionManage, Handler: h.disable},
		{Pattern: "POST /api/admin/plugins/{name}/uninstall", Permission: PermissionRemove, Handler: h.uninstall},
	} {
		backoffice.Mount(route)
	}
}

func (h *Handlers) list(w http.ResponseWriter, _ *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"plugins":             h.manager.List(),
		"two_factor_required": h.twoFactorRequired(),
	})
}

func (h *Handlers) show(w http.ResponseWriter, r *http.Request) error {
	info, err := h.manager.Info(r.PathValue("name"))
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"plugin": info})
}

type installBody struct {
	Enable        bool              `json:"enable"`
	Settings      map[string]string `json:"settings"`
	TwoFactorCode string            `json:"two_factor_code"`
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

// Switching a plugin off, installing or removing it requires a two-step code
// only when security.two_factor_plugin_manage is enabled.
func (h *Handlers) disable(w http.ResponseWriter, r *http.Request) error {
	var body confirmBody
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	name := r.PathValue("name")
	if _, err := h.manager.Info(name); err != nil {
		return translate(err)
	}
	if h.twoFactorRequired() {
		if err := h.verify(w, r, body.TwoFactorCode); err != nil {
			return err
		}
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
	if h.twoFactorRequired() {
		if err := h.verify(w, r, body.TwoFactorCode); err != nil {
			return err
		}
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
			"Managing plugins requires two-step verification enabled on your account.")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return httpx.BadRequestCode("two_factor_code_required",
			"Enter a code from your authenticator app to perform plugin operations.")
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
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"plugin":              info,
		"plugins":             h.manager.List(),
		"two_factor_required": h.twoFactorRequired(),
	})
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

func (h *Handlers) upload(w http.ResponseWriter, r *http.Request) error {
	// Parse multipart
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return httpx.BadRequest("Failed to parse form.")
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequest("Missing file.")
	}
	defer file.Close()

	if !strings.HasSuffix(header.Filename, ".zip") {
		return httpx.BadRequest("Only .zip files are supported.")
	}

	// Verify we are in an environment with the source code.
	info, err := os.Stat("plugins")
	if err != nil || !info.IsDir() {
		return httpx.BadRequestCode("no_source_tree",
			"The plugins/ directory could not be found. Uploading source code requires the server to be running from the source tree rather than as a standalone production binary.")
	}

	// Write zip to temporary file
	tmpFile, err := os.CreateTemp("", "plugin-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, file); err != nil {
		return err
	}

	// Open zip
	zr, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return httpx.BadRequest("Invalid zip file.")
	}
	defer zr.Close()

	var extractedName string
	// Validate and extract
	for _, f := range zr.File {
		// Basic zipslip protection
		if strings.Contains(f.Name, "..") {
			return httpx.BadRequest("Invalid file path in zip.")
		}
		path := filepath.Join("plugins", f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(path, 0755)
			continue
		}
		// grab the top-level folder name as plugin name
		parts := strings.Split(filepath.ToSlash(f.Name), "/")
		if len(parts) > 0 && extractedName == "" {
			extractedName = parts[0]
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dest, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(dest, rc)
		dest.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}

	if extractedName == "" {
		return httpx.BadRequest("Empty zip file.")
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": extractedName})
}
