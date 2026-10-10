package plugin

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
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
		// A super administrator's alone, whatever grants anybody else holds.
		// A package's migrations run as the database's owner at install,
		// before it is even switched on and whatever permissions it
		// declares, so uploading one is the power to rewrite any row — a
		// delegated grant made its holder a super administrator by an
		// UPDATE of their own. Switching installed plugins on and off stays
		// delegable: that runs nothing nobody has already accepted.
		{Pattern: "POST /api/admin/plugins/preview", Permission: "super_admin", Handler: h.preview},
		{Pattern: "POST /api/admin/plugins/install-package", Permission: "super_admin", Handler: h.installPackage},
		{Pattern: "POST /api/admin/plugins/{name}/install", Permission: PermissionManage, Handler: h.install},
		{Pattern: "POST /api/admin/plugins/{name}/enable", Permission: PermissionManage, Handler: h.enable},
		{Pattern: "POST /api/admin/plugins/{name}/disable", Permission: PermissionManage, Handler: h.disable},
		{Pattern: "POST /api/admin/plugins/{name}/uninstall", Permission: PermissionRemove, Handler: h.uninstall},
	} {
		backoffice.Mount(route)
	}
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"plugins":             h.viewerPlugins(r),
		"two_factor_required": h.twoFactorRequired(),
	})
}

func (h *Handlers) show(w http.ResponseWriter, r *http.Request) error {
	info, err := h.viewerPlugin(r, r.PathValue("name"))
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
	// Each setting is written under its own grant, as the shared settings route
	// writes it. plugins_manage alone is not a grant to set every key a plugin
	// declares, and a key the security grant owns is not one it may set.
	actor := auth.MustUser(r.Context())
	for key := range body.Settings {
		if !admin.CanWriteSetting(actor, key) {
			return httpx.ForbiddenCode("admin_permission_denied", "You do not have permission to access this page or perform this action.")
		}
	}
	// The operator's setting covers installing as well as switching off: an
	// install applies the plugin's migrations and can switch it on in the same
	// request, so it takes the code the other changes take.
	if h.twoFactorRequired() {
		if err := h.verify(w, r, body.TwoFactorCode); err != nil {
			return err
		}
	}
	name := r.PathValue("name")
	if err := h.manager.Install(r.Context(), h.actor(r), name, InstallOptions{
		Enable: body.Enable, Settings: body.Settings,
	}); err != nil {
		return translate(err)
	}
	return h.answer(w, r, name)
}

func (h *Handlers) enable(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if err := h.manager.Enable(r.Context(), h.actor(r), name); err != nil {
		return translate(err)
	}
	return h.answer(w, r, name)
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
	return h.answer(w, r, name)
}

func (h *Handlers) uninstall(w http.ResponseWriter, r *http.Request) error {
	var body confirmBody
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	// A purge runs the plugin's own SQL as the database's owner, and nobody
	// shows that SQL to whoever removes the plugin, so erasing the data is a
	// super administrator's, as uploading a package is. Removing a plugin and
	// keeping its data stays a delegable grant.
	if body.Purge && !auth.MustUser(r.Context()).IsSuperAdmin() {
		return httpx.ForbiddenCode("admin_permission_denied", "You do not have permission to access this page or perform this action.")
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
	// A package that was removed is not there to be described: the answer is
	// the list without it. A compiled-in plugin stays in the list, as
	// available, and is described as that.
	if info, err := h.viewerPlugin(r, name); err == nil {
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"plugin":              info,
			"plugins":             h.viewerPlugins(r),
			"two_factor_required": h.twoFactorRequired(),
		})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"plugin":              nil,
		"plugins":             h.viewerPlugins(r),
		"two_factor_required": h.twoFactorRequired(),
	})
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
func (h *Handlers) answer(w http.ResponseWriter, r *http.Request, name string) error {
	info, err := h.viewerPlugin(r, name)
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"plugin":              info,
		"plugins":             h.viewerPlugins(r),
		"two_factor_required": h.twoFactorRequired(),
	})
}

// viewerPlugins is every plugin the screen lists, each with the setting keys
// the viewer may write, so every answer that carries plugins says the same.
func (h *Handlers) viewerPlugins(r *http.Request) []Info {
	viewer := auth.MustUser(r.Context())
	infos := h.manager.List()
	for i := range infos {
		infos[i].WritableSettings = writableBy(viewer, infos[i].Name)
	}
	return infos
}

// viewerPlugin is one plugin, with the setting keys the viewer may write.
func (h *Handlers) viewerPlugin(r *http.Request, name string) (Info, error) {
	info, err := h.manager.Info(name)
	if err != nil {
		return Info{}, err
	}
	info.WritableSettings = writableBy(auth.MustUser(r.Context()), info.Name)
	return info, nil
}

// writableBy is the keys of one plugin that viewer may read and write, by the
// grant each key carries. It is the check install makes on every key it is
// sent, so a dialog that sends only these is never refused for a key the
// viewer left alone. It is never nil, so the JSON reads [] and not null.
func writableBy(viewer user.User, name string) []string {
	keys := []string{}
	for _, d := range settings.DefinitionsOf(name) {
		if admin.CanWriteSetting(viewer, d.Key) {
			keys = append(keys, d.Key)
		}
	}
	return keys
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

// preview reads an uploaded package and answers with what it asks for and
// says, holding the archive so the confirmation does not upload it twice.
// Nothing is stored and nothing runs.
func (h *Handlers) preview(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, arcx.MaxArchive+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return httpx.BadRequestCode("invalid_package", "The upload could not be read; the package may be larger than %d MiB.", arcx.MaxArchive>>20)
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequestCode("invalid_package", "Choose a plugin package to upload.")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, arcx.MaxArchive+1))
	if err != nil {
		return httpx.BadRequestCode("invalid_package", "The upload could not be read.")
	}
	info, err := h.manager.Preview(h.actor(r), raw)
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"preview": info})
}

type installPackageBody struct {
	Token         string            `json:"token"`
	Enable        bool              `json:"enable"`
	Settings      map[string]string `json:"settings"`
	TwoFactorCode string            `json:"two_factor_code"`
}

// installPackage installs what the actor previewed. Running somebody's code
// on this server is the most that can be asked of it, so it takes the same
// two-step code as switching a plugin off does, when that is required.
func (h *Handlers) installPackage(w http.ResponseWriter, r *http.Request) error {
	var body installPackageBody
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}
	if h.twoFactorRequired() {
		if err := h.verify(w, r, body.TwoFactorCode); err != nil {
			return err
		}
	}
	name, op, err := h.manager.ConfirmUpload(r.Context(), h.actor(r), body.Token, InstallOptions{
		Enable: body.Enable, Settings: body.Settings,
	})
	if err != nil {
		return translate(err)
	}
	info, err := h.viewerPlugin(r, name)
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"plugin":              info,
		"plugins":             h.viewerPlugins(r),
		"two_factor_required": h.twoFactorRequired(),
		"did":                 op,
	})
}

func translate(err error) error {
	var setting *SettingError
	var preflight *PreflightError
	switch {
	case errors.As(err, &setting):
		return httpx.BadRequestCode("plugin_setting_invalid", "Setting %q: %s", setting.Key, setting.Reason)
	case errors.Is(err, arcx.ErrInvalid):
		return httpx.BadRequestCode("invalid_package", "%s", strings.TrimPrefix(err.Error(), arcx.ErrInvalid.Error()+": "))
	case errors.As(err, &preflight):
		return httpx.BadRequestCode("plugin_cannot_install", "%s", preflight.Reason)
	case errors.Is(err, ErrNameTaken):
		return httpx.Conflict("plugin_name_taken", "A built-in plugin already has that name.")
	case errors.Is(err, ErrUnchanged):
		return httpx.Conflict("plugin_unchanged", "That package is already installed.")
	case errors.Is(err, ErrNoPending):
		return httpx.BadRequestCode("upload_expired", "That upload has expired; choose the file again.")
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
