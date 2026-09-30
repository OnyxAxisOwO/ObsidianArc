// Package arcx is the file a plugin ships as: one zip holding a manifest, the
// plugin's backend compiled to WebAssembly, the browser half, and the SQL
// that sets its tables up and takes them away.
//
// A package is data. Nothing in it runs until an administrator has looked at
// what it asks for (Manifest.Permissions) and said yes, and installing it
// copies the bytes into the database — no file is written anywhere, so
// there is no path for an archive to escape into. Removing it deletes them.
//
// This package only reads and checks. What a manifest's entries become —
// a setting the server knows, a field on accounts, a route — is the
// plugin package's business; what the backend does is the wasm package's.
package arcx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Format is the version of the archive layout and the manifest schema this
// build reads. A package written for a later one is refused rather than half
// understood.
const Format = 1

// APILevel is the version of the host interface a backend may call and the
// hooks the host calls on it. A package says which it needs (Requires.API);
// one that needs more than this build offers is refused at install, which is
// the moment the operator can still do something about it.
const APILevel = 1

// Text is one string in both of the interface's languages. An empty ZH falls
// back to EN when the interface is drawn.
type Text struct {
	EN string `json:"en"`
	ZH string `json:"zh,omitempty"`
}

// Manifest is manifest.json.
type Manifest struct {
	Format      int    `json:"arcx"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Title       Text   `json:"title"`
	Description Text   `json:"description"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`

	Requires Requires `json:"requires"`
	// What the backend is allowed to ask the host for. Shown to the
	// administrator before install and enforced on every host call: a
	// package cannot use what it did not declare.
	Permissions []string `json:"permissions,omitempty"`

	// The backend, a path inside the archive. Empty for a plugin that is
	// only settings and a browser half.
	Backend string `json:"backend,omitempty"`
	// What the host may call on the backend besides the guards, routes and
	// console commands below, which imply themselves: "describe" and
	// "decorate_invitees".
	Hooks []string `json:"hooks,omitempty"`

	Settings      []Setting        `json:"settings,omitempty"`
	CaptchaModes  []string         `json:"captcha_modes,omitempty"`
	Fields        []Field          `json:"fields,omitempty"`
	FieldRules    []FieldRule      `json:"field_rules,omitempty"`
	OAuthBindings []OAuthBinding   `json:"oauth_bindings,omitempty"`
	Guards        []Guard          `json:"guards,omitempty"`
	Routes        []Route          `json:"routes,omitempty"`
	Console       []ConsoleCommand `json:"console,omitempty"`

	// The browser half. A JavaScript module the page loads once the plugin
	// is enabled; see the plugin registry in web/src/plugins.
	UI *UI `json:"ui,omitempty"`
}

// Requires is what a package needs of the core.
type Requires struct {
	API int `json:"api"`
}

// The permissions a backend can hold. Each names one family of host calls.
const (
	// db.query, db.exec and transactions, against the instance's database:
	// the accounts, the cards, the invitations — everything. Granting it is
	// granting the plugin the same reach the core's own code has.
	PermDB = "db"
	// http.fetch to any address the server can reach.
	PermNetwork = "network"
	// sessions.revoke_user: ending an account's sign-ins.
	PermSessions = "sessions"
	// users.count_active_admins, users.set_status and users.delete: the
	// operations that end or suspend an account, done by the same rules the
	// backoffice's own are.
	PermUsers = "users"
	// cards.revoke_available: taking back reset cards an account holds
	// unspent.
	PermCards = "cards"
	// notify.push: putting something in an account's inbox.
	PermNotify = "notify"
	// security.record: writing to the security log.
	PermSecurityLog = "security_log"
	// console.call: running an administrative endpoint as the operator who
	// ran a console command.
	PermConsole = "console"
)

// Permissions lists them, in the order an install dialog shows them.
var Permissions = []string{PermDB, PermNetwork, PermUsers, PermCards, PermSessions, PermNotify, PermSecurityLog, PermConsole}

// Hooks a manifest may list.
const (
	// The backend says what the browser and the page's policy should be told
	// right now (see the SDK's OnDescribe); asked again whenever one of the
	// plugin's settings changes.
	HookDescribe = "describe"
	// The backend adds to the rows of an inviter's own invitee list.
	HookDecorateInvitees = "decorate_invitees"
)

// Setting is a setting the plugin owns: unknown to the server until the
// plugin is installed, and forgotten when it is removed with its data.
type Setting struct {
	Key     string `json:"key"`
	Default string `json:"default,omitempty"`
	// Write-only: redacted out of every response.
	Secret bool `json:"secret,omitempty"`
	// The backoffice grant that may read and write it, as the admin module
	// spells them ("security", "settings,invites"). Empty falls back to the
	// key's prefix, the rule the core's own settings follow.
	Permission string `json:"permission,omitempty"`
	// What a value may be. Both empty accepts anything under the length cap
	// every setting has.
	Enum    []string `json:"enum,omitempty"`
	Pattern string   `json:"pattern,omitempty"`

	// How the install dialog asks for it. Initial settings are the ones an
	// operator is asked about before the plugin runs; the rest are left to
	// its own page.
	Initial bool `json:"initial,omitempty"`
	Label   Text `json:"label,omitempty"`
	Hint    Text `json:"hint,omitempty"`
}

// Field is a column the plugin adds to the accounts table. The column itself
// comes from the plugin's own migration.
type Field struct {
	Key        string `json:"key"`
	Unique     bool   `json:"unique,omitempty"`
	Searchable bool   `json:"searchable,omitempty"`
	// A value has to match it whole. Empty accepts anything.
	Pattern string `json:"pattern,omitempty"`
}

// FieldRule says which setting decides whether sign-up asks for a field:
// "off", "optional" or "required".
type FieldRule struct {
	Field   string `json:"field"`
	Setting string `json:"setting"`
}

// OAuthBinding says that an OpenID Connect provider's subject, when it has
// this shape, is the value of an account field.
type OAuthBinding struct {
	Provider string `json:"provider"`
	Field    string `json:"field"`
	Pattern  string `json:"pattern"`
}

// Guard is a check the backend runs in front of sign-up or sign-in.
type Guard struct {
	// "register" or "login".
	Action string `json:"action"`
	// The key the browser's token rides under, and what the backend
	// registers its handler as.
	Name string `json:"name"`
	// The security log's event name for a refusal.
	Event string `json:"event"`
}

// Route is an endpoint the backend serves.
type Route struct {
	// A full ServeMux pattern: "POST /api/admin/x/demo/things/{id}".
	Pattern string `json:"pattern"`
	// "admin" mounts it behind the backoffice's checks (session, two-step,
	// Permission); "public" mounts it as it is, and the backend does its own.
	Access string `json:"access"`
	// The backoffice grant an "admin" route needs, as the admin module
	// spells them. Required for admin routes.
	Permission string `json:"permission,omitempty"`
}

// Access values.
const (
	AccessAdmin  = "admin"
	AccessPublic = "public"
)

// ConsoleCommand is a command the plugin adds to the administration console.
// Its Run is the backend's; everything the console draws about it is here.
type ConsoleCommand struct {
	Name        string        `json:"name"`
	Group       string        `json:"group,omitempty"`
	Summary     Text          `json:"summary"`
	Usage       string        `json:"usage,omitempty"`
	Help        Text          `json:"help,omitempty"`
	Args        []ConsoleArg  `json:"args,omitempty"`
	Flags       []ConsoleFlag `json:"flags,omitempty"`
	Examples    []string      `json:"examples,omitempty"`
	SeeAlso     []string      `json:"see_also,omitempty"`
	Permission  string        `json:"permission,omitempty"`
	Destructive bool          `json:"destructive,omitempty"`
	Endpoints   []string      `json:"endpoints,omitempty"`
}

// ConsoleArg is a positional argument.
type ConsoleArg struct {
	Name     string `json:"name"`
	Hint     Text   `json:"hint,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// ConsoleFlag is a flag. Value names its argument ("N", "MODE"); empty is a
// switch.
type ConsoleFlag struct {
	Name  string `json:"name"`
	Hint  Text   `json:"hint,omitempty"`
	Value string `json:"value,omitempty"`
}

// UI is the browser half.
type UI struct {
	// A path inside the archive, under web/: an ES module whose default
	// export takes the host object and returns the plugin's declaration.
	Module string `json:"module"`
}

var (
	nameRE       = regexp.MustCompile(`^[a-z][a-z0-9]{1,31}$`)
	versionRE    = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,3}([-+][0-9A-Za-z.-]+)?$`)
	settingKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	fieldKeyRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
	guardNameRE  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	modeRE       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	grantRE      = regexp.MustCompile(`^[a-z_]+(,[a-z_]+)*$`)
	consoleRE    = regexp.MustCompile(`^[a-z][a-z0-9-]*( [a-z][a-z0-9-]*)*$`)
)

// ParseManifest reads manifest.json, refusing anything it does not know:
// a misspelt key is a feature that silently does not exist, and this is the
// only moment anyone is looking.
func ParseManifest(raw []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if dec.More() {
		return nil, errors.New("manifest.json: trailing data")
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	return &m, nil
}

// Validate is everything about a manifest that can be checked without the
// server it will be installed on: shapes, spellings and duplicates. What
// depends on the server — a setting key that a core setting already owns, a
// route that collides with one — is the installer's to check.
func (m *Manifest) Validate() error {
	if m.Format != Format {
		return fmt.Errorf("arcx is %d, this build reads %d", m.Format, Format)
	}
	if !nameRE.MatchString(m.Name) {
		return fmt.Errorf("name %q must be 2–32 lowercase letters and digits, starting with a letter", m.Name)
	}
	if !versionRE.MatchString(m.Version) {
		return fmt.Errorf("version %q is not a version", m.Version)
	}
	if strings.TrimSpace(m.Title.EN) == "" {
		return errors.New("title.en is required")
	}
	if m.Requires.API < 1 {
		return errors.New("requires.api is required")
	}
	if m.Requires.API > APILevel {
		return fmt.Errorf("needs plugin API %d and this server offers %d", m.Requires.API, APILevel)
	}
	if err := m.validatePermissions(); err != nil {
		return err
	}
	if m.Backend != "" && !strings.HasSuffix(m.Backend, ".wasm") {
		return fmt.Errorf("backend %q is not a .wasm file", m.Backend)
	}
	for _, hook := range m.Hooks {
		switch hook {
		case HookDescribe, HookDecorateInvitees:
		default:
			return fmt.Errorf("unknown hook %q", hook)
		}
	}
	if m.UI != nil && !strings.HasPrefix(m.UI.Module, "web/") {
		return fmt.Errorf("ui.module %q must be under web/", m.UI.Module)
	}
	if err := m.validateSettings(); err != nil {
		return err
	}
	for _, mode := range m.CaptchaModes {
		if !modeRE.MatchString(mode) {
			return fmt.Errorf("captcha mode %q is not a valid name", mode)
		}
	}
	if err := m.validateFields(); err != nil {
		return err
	}
	if err := m.validateGuards(); err != nil {
		return err
	}
	if err := m.validateRoutes(); err != nil {
		return err
	}
	if err := m.validateConsole(); err != nil {
		return err
	}
	needsBackend := len(m.Guards) > 0 || len(m.Routes) > 0 || len(m.Console) > 0 || len(m.Hooks) > 0
	if needsBackend && m.Backend == "" {
		return errors.New("guards, routes, console commands and hooks need a backend")
	}
	return nil
}

func (m *Manifest) validatePermissions() error {
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		known := false
		for _, k := range Permissions {
			known = known || p == k
		}
		if !known {
			return fmt.Errorf("unknown permission %q", p)
		}
		if seen[p] {
			return fmt.Errorf("permission %q listed twice", p)
		}
		seen[p] = true
	}
	return nil
}

// Has reports whether the manifest declares permission p.
func (m *Manifest) Has(p string) bool {
	for _, have := range m.Permissions {
		if have == p {
			return true
		}
	}
	return false
}

func (m *Manifest) validateSettings() error {
	seen := map[string]bool{}
	for _, s := range m.Settings {
		if !settingKeyRE.MatchString(s.Key) || len(s.Key) > 80 {
			return fmt.Errorf("setting key %q is not a dotted lowercase name", s.Key)
		}
		if seen[s.Key] {
			return fmt.Errorf("setting %q defined twice", s.Key)
		}
		seen[s.Key] = true
		if s.Permission != "" && !grantRE.MatchString(s.Permission) {
			return fmt.Errorf("setting %s: permission %q is not a grant list", s.Key, s.Permission)
		}
		if s.Pattern != "" {
			if _, err := regexp.Compile(s.Pattern); err != nil {
				return fmt.Errorf("setting %s: pattern: %v", s.Key, err)
			}
		}
		if len(s.Enum) > 0 && s.Default != "" {
			ok := false
			for _, v := range s.Enum {
				ok = ok || v == s.Default
			}
			if !ok {
				return fmt.Errorf("setting %s: default %q is not one of its values", s.Key, s.Default)
			}
		}
		if s.Secret && s.Default != "" {
			return fmt.Errorf("setting %s: a secret has no default", s.Key)
		}
	}
	return nil
}

func (m *Manifest) validateFields() error {
	fields := map[string]bool{}
	for _, f := range m.Fields {
		if !fieldKeyRE.MatchString(f.Key) {
			return fmt.Errorf("field key %q is not a valid column name", f.Key)
		}
		if fields[f.Key] {
			return fmt.Errorf("field %q defined twice", f.Key)
		}
		fields[f.Key] = true
		if f.Pattern != "" {
			if _, err := regexp.Compile(f.Pattern); err != nil {
				return fmt.Errorf("field %s: pattern: %v", f.Key, err)
			}
		}
	}
	settings := map[string]bool{}
	for _, s := range m.Settings {
		settings[s.Key] = true
	}
	for _, rule := range m.FieldRules {
		if !fields[rule.Field] {
			return fmt.Errorf("field rule names field %q, which the plugin does not define", rule.Field)
		}
		if !settings[rule.Setting] {
			return fmt.Errorf("field rule names setting %q, which the plugin does not define", rule.Setting)
		}
	}
	for _, b := range m.OAuthBindings {
		if !fields[b.Field] {
			return fmt.Errorf("oauth binding names field %q, which the plugin does not define", b.Field)
		}
		if b.Provider == "" || b.Pattern == "" {
			return errors.New("an oauth binding needs a provider and a pattern")
		}
		if _, err := regexp.Compile(b.Pattern); err != nil {
			return fmt.Errorf("oauth binding %s: pattern: %v", b.Field, err)
		}
	}
	return nil
}

func (m *Manifest) validateGuards() error {
	seen := map[string]bool{}
	for _, g := range m.Guards {
		if g.Action != "register" && g.Action != "login" {
			return fmt.Errorf("guard %q: action must be register or login", g.Name)
		}
		if !guardNameRE.MatchString(g.Name) {
			return fmt.Errorf("guard name %q is not valid", g.Name)
		}
		if g.Event == "" {
			return fmt.Errorf("guard %q needs an event", g.Name)
		}
		key := g.Action + ":" + g.Name
		if seen[key] {
			return fmt.Errorf("guard %s defined twice", key)
		}
		seen[key] = true
	}
	return nil
}

func (m *Manifest) validateRoutes() error {
	seen := map[string]bool{}
	for _, r := range m.Routes {
		method, path, ok := strings.Cut(r.Pattern, " ")
		if !ok || method == "" || !strings.HasPrefix(path, "/") {
			return fmt.Errorf("route %q must be \"METHOD /path\"", r.Pattern)
		}
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return fmt.Errorf("route %q: method %s is not supported", r.Pattern, method)
		}
		if seen[r.Pattern] {
			return fmt.Errorf("route %q defined twice", r.Pattern)
		}
		seen[r.Pattern] = true
		switch r.Access {
		case AccessAdmin:
			if !strings.HasPrefix(path, "/api/admin/") {
				return fmt.Errorf("admin route %q must live under /api/admin/", r.Pattern)
			}
			if r.Permission == "" || !grantRE.MatchString(r.Permission) {
				return fmt.Errorf("admin route %q needs a permission grant", r.Pattern)
			}
		case AccessPublic:
			if !strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/api/admin/") {
				return fmt.Errorf("public route %q must live under /api/ and outside /api/admin/", r.Pattern)
			}
			if r.Permission != "" {
				return fmt.Errorf("public route %q has no permission to check", r.Pattern)
			}
		default:
			return fmt.Errorf("route %q: access must be admin or public", r.Pattern)
		}
	}
	return nil
}

func (m *Manifest) validateConsole() error {
	seen := map[string]bool{}
	for _, c := range m.Console {
		if !consoleRE.MatchString(c.Name) {
			return fmt.Errorf("console command %q is not a valid name", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("console command %q defined twice", c.Name)
		}
		seen[c.Name] = true
		if strings.TrimSpace(c.Summary.EN) == "" {
			return fmt.Errorf("console command %q needs a summary", c.Name)
		}
		if c.Permission != "" && c.Permission != "super_admin" && !grantRE.MatchString(c.Permission) {
			return fmt.Errorf("console command %q: permission %q is not a grant list", c.Name, c.Permission)
		}
	}
	return nil
}
