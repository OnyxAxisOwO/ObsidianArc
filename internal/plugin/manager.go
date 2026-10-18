package plugin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// State is where a compiled-in plugin stands on this instance.
type State string

const (
	// Compiled in and not installed: none of its migrations run for it,
	// none of its settings known, none of its hooks run.
	StateAvailable State = "available"
	StateEnabled   State = "enabled"
	// Installed — its tables and settings are there — and switched off.
	StateDisabled State = "disabled"
)

// What plugin_installs stores for an uninstalled plugin (see the migration).
const stateRemoved = "removed"

var (
	ErrUnknown        = errors.New("plugin: no such plugin in this build")
	ErrInstalled      = errors.New("plugin: already installed")
	ErrNotInstalled   = errors.New("plugin: not installed")
	ErrAlreadyInState = errors.New("plugin: already in that state")
)

// SettingError is an initial setting the install refused.
type SettingError struct {
	Key    string
	Reason string
}

func (e *SettingError) Error() string { return fmt.Sprintf("plugin: setting %s: %s", e.Key, e.Reason) }

// Actor is who asked for a change, for the plugin row and the security log.
type Actor struct {
	ID       string
	Username string
	IP       string
}

// InstallOptions is what the install dialog asks.
type InstallOptions struct {
	// Switch it on in the same step. Off installs it switched off, for an
	// operator who wants to finish configuring it before anybody meets it.
	Enable bool
	// The plugin's own settings, written with it — only keys it defines.
	Settings map[string]string
}

// Manager keeps each compiled-in plugin's state: the plugin_installs table,
// and an in-memory copy every extension point's gate reads on each request.
//
// Transitions are check-then-write — "installed?" then "install" — so each
// runs in a transaction holding the plugin's own row, the way the core's
// per-account invariants hold the account's row: a second process against
// the same database waits there rather than running the migrations twice.
//
// The in-memory copy is this process's. A second instance learns a change
// when it restarts; until then it keeps serving the state it booted with,
// which for an uninstall that dropped a column means its queries naming the
// column fail. The deployment notes allow a second instance; they do not
// promise the backoffice's plugin screen is safe to use with one running.
type Manager struct {
	db       *database.DB
	settings *settings.Service
	users    *user.Store
	security *securityevents.Store

	states atomic.Pointer[map[string]record]
	// Orders this process's own transitions, so the copy above ends in the
	// state the last committed one left. The database lock is what makes
	// the transitions safe; this only keeps two of them from writing the
	// copy back in the opposite order they committed.
	mu sync.Mutex

	// Set by the server once the plugins are set up, for the manifest's
	// view of what each one attached.
	host *Host

	// The plugins that are packages rather than compiled in, by name.
	// Replaced whole under mu, read without it by every request that is
	// routed to one.
	pkgs atomic.Pointer[map[string]*loaded]
	// The backends' engine, made when the first one needs it.
	eng      *wasm.Engine
	engOnce  sync.Once
	cacheDir string
	// The console the packages' commands are added to, once there is one.
	console *console.Console
	// Where requests for a package's routes are served from; see routes.go.
	router atomic.Pointer[routeTable]
	// Uploads the operator has looked at and not yet confirmed.
	pending pendingUploads
	// The installed packages whose archive could not be read at boot. Set by
	// loadPackages; a package that is installed again, from a readable archive,
	// is in pkgs and so stops being listed here.
	unreadable atomic.Pointer[map[string]bool]
}

type record struct {
	State       State
	Version     string
	InstalledAt int64
	InstalledBy string
	UpdatedAt   int64
	UpdatedBy   string
}

func NewManager(db *database.DB, set *settings.Service, users *user.Store, security *securityevents.Store) *Manager {
	m := &Manager{db: db, settings: set, users: users, security: security}
	empty := map[string]record{}
	m.states.Store(&empty)
	none := map[string]*loaded{}
	m.pkgs.Store(&none)
	return m
}

// SetCacheDir names the directory compiled backends are kept in, so that
// compiling one again is reading a file. It has to be called before the first
// backend is loaded; a Manager without one compiles every time.
func (m *Manager) SetCacheDir(dir string) { m.cacheDir = dir }

// Warm compiles the backends of the packages that are switched on, one after
// another in the background, so the first request that needs one does not wait
// for it: on an ordinary server core a compile is most of a minute for three.
// With a cache directory the result is kept, and this is a few file reads.
func (m *Manager) Warm() {
	go func() {
		ctx := context.Background()
		for _, l := range *m.pkgs.Load() {
			if l.backend == nil || (*m.states.Load())[l.name].State != StateEnabled {
				continue
			}
			if err := l.backend.Ready(ctx); err != nil {
				slog.Warn("plugin backend did not compile", "plugin", l.name, "error", err)
			}
		}
	}()
}

// engine is the wasm engine the packages' backends run on.
func (m *Manager) engine() *wasm.Engine {
	m.engOnce.Do(func() { m.eng = wasm.NewEngine(wasm.Limits{CacheDir: m.cacheDir}) })
	return m.eng
}

// plugin is the compiled-in plugin called name, or the package's stand-in for
// an installed one: whichever this server has.
func (m *Manager) plugin(name string) (Plugin, bool) {
	if p, ok := Lookup(name); ok {
		return p, true
	}
	if l := (*m.pkgs.Load())[name]; l != nil {
		return packagePlugin{l}, true
	}
	// Installed, but nothing of it loads: its archive could not be read at
	// boot, or it collides with another's migrations. It can still be switched
	// off and removed, which is what the operator needs to do with it. Keyed on
	// the boot-time set rather than the recorded state, because a row for a
	// plugin this build does not carry is not ours to stand in for.
	if u := m.unreadable.Load(); u != nil && (*u)[name] {
		return unreadablePlugin{name: name}, true
	}
	return nil, false
}

// unreadablePlugin stands in for an installed package whose archive cannot be read:
// there are no migrations, purge scripts or seams to take away, only the record.
type unreadablePlugin struct{ name string }

func (u unreadablePlugin) Name() string { return u.name }

func (u unreadablePlugin) Manifest() Manifest {
	return Manifest{Title: Text{EN: u.name, ZH: u.name}}
}

func (unreadablePlugin) Setup(*Host) error { return nil }

func (m *Manager) loadedPackage(name string) *loaded { return (*m.pkgs.Load())[name] }

// Gate is the question every extension point asks.
func (m *Manager) Gate() plugingate.Gate { return m.Enabled }

// Enabled reports whether name is installed and switched on. The core, ""
// is always on.
func (m *Manager) Enabled(name string) bool {
	if name == "" {
		return true
	}
	if (*m.states.Load())[name].State != StateEnabled {
		return false
	}
	// A package whose archive could not be read at boot, or whose migrations
	// collide with another's, has nothing loaded behind it. Switched on, it
	// would be a gate open on nothing, so it is off.
	// A row for a plugin this build does not carry is different: it is read
	// as it stands, and there is simply nothing registered under it.
	if stuck := m.unreadable.Load(); stuck != nil && (*stuck)[name] && (*m.pkgs.Load())[name] == nil {
		return false
	}
	if l := (*m.pkgs.Load())[name]; l != nil && l.faulted() != "" {
		return false
	}
	return true
}

// State is name's state; a compiled-in plugin never decided is available.
func (m *Manager) State(name string) State {
	if r, ok := (*m.states.Load())[name]; ok && r.State != "" {
		return r.State
	}
	return StateAvailable
}

// Load reads the table and brings it up to date with this build, before the
// server takes its first request:
//
//   - A compiled-in plugin with no row is adopted — recorded as enabled — if
//     the instance already carries its traces: a migration of its recorded,
//     or a setting of its stored. That is an instance which ran the plugin
//     before plugins could be switched, or ran it as part of the core
//     before that, and it carries on as it was. Anything else stays
//     available until an administrator installs it.
//   - Every installed plugin's migrations run, so a new build of a plugin
//     that brings a new migration applies it the way the core's do.
func (m *Manager) Load(ctx context.Context) error {
	rows, err := m.read(ctx, m.db)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, p := range All() {
		name := p.Name()
		if _, decided := rows[name]; decided {
			continue
		}
		traces, err := m.traces(ctx, p)
		if err != nil {
			return err
		}
		if !traces {
			continue
		}
		if _, err := m.db.Exec(ctx,
			`INSERT INTO plugin_installs (name, state, version, installed_at, installed_by, updated_at, updated_by)
			 VALUES (?, ?, ?, ?, '', ?, '')
			 ON CONFLICT (name) DO NOTHING`,
			name, string(StateEnabled), p.Manifest().Version, now, now); err != nil {
			return fmt.Errorf("plugin: adopt %s: %w", name, err)
		}
	}
	if rows, err = m.read(ctx, m.db); err != nil {
		return err
	}

	var installed []fs.FS
	for _, p := range All() {
		if r := rows[p.Name()]; r.State == StateEnabled || r.State == StateDisabled {
			if dir := migrationsOf(p); dir != nil {
				installed = append(installed, dir)
			}
		}
	}
	if len(installed) > 0 {
		if _, err := m.db.Migrate(ctx, installed...); err != nil {
			return err
		}
	}
	m.states.Store(&rows)
	if err := m.loadPackages(ctx); err != nil {
		return err
	}
	m.users.RefreshFields()
	return nil
}

// traces reports whether the database already holds something of p's.
func (m *Manager) traces(ctx context.Context, p Plugin) (bool, error) {
	if dir := migrationsOf(p); dir != nil {
		versions, err := database.Versions(dir)
		if err != nil {
			return false, err
		}
		applied, err := database.Applied(ctx, m.db, versions)
		if err != nil {
			return false, err
		}
		for _, done := range applied {
			if done {
				return true, nil
			}
		}
	}
	return m.settings.Stored(m.settingKeys(p.Name())), nil
}

// read is the table as states: a removed row reads as available.
func (m *Manager) read(ctx context.Context, q database.Queryer) (map[string]record, error) {
	rows, err := q.Query(ctx,
		`SELECT name, state, version, installed_at, installed_by, updated_at, updated_by FROM plugin_installs`)
	if err != nil {
		return nil, fmt.Errorf("plugin: read states: %w", err)
	}
	defer rows.Close()
	out := map[string]record{}
	for rows.Next() {
		var name, state string
		var r record
		if err := rows.Scan(&name, &state, &r.Version, &r.InstalledAt, &r.InstalledBy, &r.UpdatedAt, &r.UpdatedBy); err != nil {
			return nil, fmt.Errorf("plugin: scan state: %w", err)
		}
		r.State = State(state)
		if state == stateRemoved {
			r.State = StateAvailable
		}
		out[name] = r
	}
	return out, rows.Err()
}

// lock takes name's row for the rest of tx — inserting it, as removed, when
// the plugin has none yet, since a row that does not exist cannot be
// locked — and reads the state under the lock. The upsert changes nothing
// on a row that exists; it is the instance-wide spelling of the settings
// row lock, on this table's row.
func (m *Manager) lock(ctx context.Context, tx *database.Tx, name string) (State, error) {
	if _, err := tx.Exec(ctx,
		`INSERT INTO plugin_installs (name, state, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (name) DO UPDATE SET updated_at = plugin_installs.updated_at`,
		name, stateRemoved, time.Now().UnixMilli()); err != nil {
		return "", fmt.Errorf("plugin: lock %s: %w", name, err)
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM plugin_installs WHERE name = ?`, name).Scan(&state); err != nil {
		return "", fmt.Errorf("plugin: read %s: %w", name, err)
	}
	if state == stateRemoved {
		return StateAvailable, nil
	}
	return State(state), nil
}

// Install installs name: its migrations, its first settings and its row in
// one transaction, so a failure anywhere leaves it as it was.
func (m *Manager) Install(ctx context.Context, actor Actor, name string, opts InstallOptions) error {
	p, ok := Lookup(name)
	if !ok {
		return ErrUnknown
	}
	values, err := checkSettings(name, opts.Settings)
	if err != nil {
		return err
	}
	state := StateDisabled
	decision := "install"
	if opts.Enable {
		state, decision = StateEnabled, "install_enable"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UnixMilli()
	var rec record
	err = m.db.Tx(ctx, func(tx *database.Tx) error {
		current, err := m.lock(ctx, tx, name)
		if err != nil {
			return err
		}
		if current != StateAvailable {
			return ErrInstalled
		}
		if dir := migrationsOf(p); dir != nil {
			if _, err := m.db.MigrateIn(ctx, tx, dir); err != nil {
				return err
			}
		}
		if err := m.settings.WriteMany(ctx, tx, values); err != nil {
			return err
		}
		if rec, err = writeInstallRow(ctx, tx, name, state, p.Manifest().Version, now, actor.ID); err != nil {
			return err
		}
		return m.audit(ctx, tx, actor, name, decision)
	})
	if err != nil {
		return err
	}
	m.settings.Remember(values)
	m.set(name, rec)
	return nil
}

// writeInstallRow marks name installed in state, and is the record the manager
// keeps for it once the transaction commits. A compiled-in plugin and a package
// are installed through it both, so a column added to plugin_installs is written
// by one statement rather than two that can fall out of step.
func writeInstallRow(ctx context.Context, tx *database.Tx, name string, state State, version string, now int64, actorID string) (record, error) {
	if _, err := tx.Exec(ctx,
		`UPDATE plugin_installs SET state = ?, version = ?, installed_at = ?, installed_by = ?,
		 updated_at = ?, updated_by = ? WHERE name = ?`,
		string(state), version, now, actorID, now, actorID, name); err != nil {
		return record{}, fmt.Errorf("plugin: install %s: %w", name, err)
	}
	return record{State: state, Version: version, InstalledAt: now, InstalledBy: actorID, UpdatedAt: now, UpdatedBy: actorID}, nil
}

// Enable switches an installed plugin on.
func (m *Manager) Enable(ctx context.Context, actor Actor, name string) error {
	return m.toggle(ctx, actor, name, StateEnabled, "enable")
}

// Disable switches an installed plugin off, keeping everything it has.
func (m *Manager) Disable(ctx context.Context, actor Actor, name string) error {
	return m.toggle(ctx, actor, name, StateDisabled, "disable")
}

func (m *Manager) toggle(ctx context.Context, actor Actor, name string, to State, decision string) error {
	if _, ok := m.plugin(name); !ok {
		return ErrUnknown
	}
	// Switching on needs the backend compiled, and one given back while idle
	// takes most of a minute to compile again. That happens before the lock is
	// taken, so the other plugin operations do not wait behind it; the describe
	// below then runs on code that is already warm.
	//
	// A backend that does not compile must refuse the switch. The install
	// compiled it once, but a core upgrade or a restored backup can leave it
	// unable to compile, and switching it on must not turn that into an outage
	// of every guard it stands in front of. The install refuses the same way.
	if l := m.loadedPackage(name); l != nil && to == StateEnabled && m.State(name) != StateEnabled && l.backend != nil {
		if err := l.backend.Ready(ctx); err != nil {
			return preflight("the backend does not load: %v", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UnixMilli()
	var kept record
	err := m.db.Tx(ctx, func(tx *database.Tx) error {
		current, err := m.lock(ctx, tx, name)
		if err != nil {
			return err
		}
		switch current {
		case StateAvailable:
			return ErrNotInstalled
		case to:
			return ErrAlreadyInState
		}
		if _, err := tx.Exec(ctx,
			`UPDATE plugin_installs SET state = ?, updated_at = ?, updated_by = ? WHERE name = ?`,
			string(to), now, actor.ID, name); err != nil {
			return fmt.Errorf("plugin: %s %s: %w", decision, name, err)
		}
		if err := tx.QueryRow(ctx,
			`SELECT version, installed_at, installed_by FROM plugin_installs WHERE name = ?`, name,
		).Scan(&kept.Version, &kept.InstalledAt, &kept.InstalledBy); err != nil {
			return err
		}
		return m.audit(ctx, tx, actor, name, decision)
	})
	if err != nil {
		return err
	}
	kept.State, kept.UpdatedAt, kept.UpdatedBy = to, now, actor.ID
	// What a package's backend tells the browser and the page's policy is
	// asked for before the gate opens, so the first request after it does
	// not see an empty answer.
	if l := m.loadedPackage(name); l != nil && to == StateEnabled {
		m.refreshDescribe(l)
	}
	m.set(name, kept)
	return nil
}

// Uninstall takes an installed plugin off the instance. With purge its data
// goes too — its settings rows, and the tables and columns its own purge
// scripts drop — and its migrations are forgotten, so installing it again
// starts empty. Without, everything stays where it is for a reinstall to
// pick back up.
//
// The plugin is switched off in memory before the transaction starts:
// purging can drop a column the accounts query names, and every request
// from here on has to stop naming it before it goes. A failed uninstall
// puts the state back.
func (m *Manager) Uninstall(ctx context.Context, actor Actor, name string, purge bool) error {
	// Which version is installed is read under the lock an update holds while it
	// swaps the new one in. Read before it, a concurrent update would leave this
	// uninstall purging and detaching the old version while the new one's seams
	// and backend stay behind.
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plugin(name)
	if !ok {
		return ErrUnknown
	}
	// Refused before the plugin is switched off, so a refusal leaves it as it was.
	// An unreadable archive has no purge scripts, migration records or settings
	// to walk, so a purge would report success and leave them all behind for a
	// reinstall to reuse.
	if _, unreadable := p.(unreadablePlugin); unreadable && purge {
		return preflight("%s's archive cannot be read, so its purge cannot remove its settings or tables; uninstall it without --purge", name)
	}
	pkg := m.loadedPackage(name)
	before := (*m.states.Load())[name]
	if before.State == StateEnabled {
		off := before
		off.State = StateDisabled
		m.set(name, off)
	}
	keys := m.settingKeys(name)
	decision := "uninstall"
	if purge {
		decision = "uninstall_purge"
	}
	err := m.db.Tx(ctx, func(tx *database.Tx) error {
		current, err := m.lock(ctx, tx, name)
		if err != nil {
			return err
		}
		if current == StateAvailable {
			return ErrNotInstalled
		}
		if purge {
			if dir := purgeOf(p); dir != nil {
				if err := database.RunScripts(ctx, tx, dir); err != nil {
					return err
				}
				if migrations := migrationsOf(p); migrations != nil {
					versions, err := database.Versions(migrations)
					if err != nil {
						return err
					}
					if err := database.Unrecord(ctx, tx, versions); err != nil {
						return err
					}
				}
			}
			if err := m.settings.DeleteMany(ctx, tx, keys); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE plugin_installs SET state = ?, updated_at = ?, updated_by = ? WHERE name = ?`,
			stateRemoved, time.Now().UnixMilli(), actor.ID, name); err != nil {
			return fmt.Errorf("plugin: uninstall %s: %w", name, err)
		}
		// A package is not something an uninstall leaves behind for a
		// reinstall to find: it goes, and what the operator dragged in is
		// gone from the list. Its data stays unless they asked otherwise. This
		// holds for an archive that could not be read at boot too, which has
		// no loaded copy but still has its row.
		if _, builtin := Lookup(name); !builtin {
			if _, err := tx.Exec(ctx, `DELETE FROM plugin_packages WHERE name = ?`, name); err != nil {
				return fmt.Errorf("plugin: remove package %s: %w", name, err)
			}
		}
		return m.audit(ctx, tx, actor, name, decision)
	})
	if err != nil {
		m.set(name, before)
		return err
	}
	if purge {
		m.settings.Forget(keys)
	}
	if pkg != nil {
		m.detachPackage(pkg)
	}
	m.set(name, record{State: StateAvailable, UpdatedAt: time.Now().UnixMilli(), UpdatedBy: actor.ID})
	return nil
}

// set replaces one plugin's entry in the copy the gates read, and tells the
// accounts store, whose column list is the one consumer that caches what
// the gate says.
func (m *Manager) set(name string, r record) {
	next := map[string]record{}
	for key, value := range *m.states.Load() {
		next[key] = value
	}
	next[name] = r
	m.states.Store(&next)
	m.users.RefreshFields()
}

func (m *Manager) audit(ctx context.Context, q database.Queryer, actor Actor, name, decision string) error {
	if m.security == nil {
		return nil
	}
	return m.security.Record(ctx, q, securityevents.Event{
		Event: securityevents.EventPlugin, Severity: securityevents.SeverityInfo,
		UserID: actor.ID, Username: actor.Username, ActorID: actor.ID, ActorUsername: actor.Username,
		IP: actor.IP, Source: "backoffice", Decision: decision, Reason: name,
	})
}

// settingKeys is the keys a plugin owns on this server, compiled in or
// installed. Every caller goes through the manager's own registry, which holds
// both kinds; a package cannot take a compiled-in name, so for those the answer
// is the same.
func (m *Manager) settingKeys(name string) []string {
	var out []string
	for _, d := range m.settings.DefinitionsOf(name) {
		out = append(out, d.Key)
	}
	return out
}

// checkSettings holds an install's first settings to the plugin's own
// definitions: a key it does not define is refused rather than stored, and
// each value passes the definition's validator. Only a compiled-in plugin is
// installed through Install, so the process-wide definitions are the plugin's
// own here; a package's are checked by checkAgainst with the manifest it
// carries, because the package is not registered yet.
func checkSettings(name string, values map[string]string) (map[string]string, error) {
	return checkAgainst(settings.DefinitionsOf(name), values)
}

// maxSettingValueBytes bounds one plugin setting. Every value is held in memory
// and read on the requests that need it, so the bound is set well below what a
// settings row will take.
const maxSettingValueBytes = 8 * 1024

func checkAgainst(definitions []settings.Definition, values map[string]string) (map[string]string, error) {
	defined := map[string]settings.Definition{}
	for _, d := range definitions {
		defined[d.Key] = d
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		d, ok := defined[key]
		if !ok {
			return nil, &SettingError{Key: key, Reason: "not a setting of this plugin"}
		}
		if len(value) > maxSettingValueBytes {
			return nil, &SettingError{Key: key, Reason: "too long"}
		}
		if d.Validate != nil {
			if err := d.Validate(value); err != nil {
				return nil, &SettingError{Key: key, Reason: err.Error()}
			}
		}
		out[key] = value
	}
	return out, nil
}

// Info is one plugin as the backoffice lists it.
type Info struct {
	Name          string        `json:"name"`
	Manifest      Manifest      `json:"manifest"`
	State         State         `json:"state"`
	Installed     InstallRecord `json:"installed"`
	Contributions Contributions `json:"contributions"`
	// A row for a plugin this build does not carry: installed on this
	// instance by a build that did, and inert until one does again.
	Missing bool `json:"missing,omitempty"`

	// "builtin" for a plugin compiled into this binary, "package" for one
	// installed from an archive.
	Kind string `json:"kind"`
	// Of a package: what it asked for and was granted, which archive it is,
	// where it came from, and whether it has a browser half.
	Permissions []string `json:"permissions"`
	SHA256      string   `json:"sha256,omitempty"`
	Size        int      `json:"size,omitempty"`
	Source      string   `json:"source,omitempty"`
	HasUI       bool     `json:"has_ui,omitempty"`
	// Why a package that is installed is not running, when it is not.
	Fault string `json:"fault,omitempty"`
	// The setting keys the viewer of one answer may read and write, each by its
	// own grant. Only a request knows who is asking, so the handlers fill this
	// in; the manager's answers leave it empty.
	WritableSettings []string `json:"writable_settings"`
}

// InstallRecord is who installed it and when, and the version they did.
type InstallRecord struct {
	Version   string `json:"version,omitempty"`
	At        int64  `json:"at,omitempty"`
	By        string `json:"by,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// Contributions is what a plugin attached, read off the extension points.
type Contributions struct {
	Settings     []SettingInfo `json:"settings"`
	Fields       []string      `json:"fields"`
	Guards       []string      `json:"guards"`
	CaptchaModes []string      `json:"captcha_modes"`
	AdminRoutes  []RouteInfo   `json:"admin_routes"`
	PublicRoutes []string      `json:"public_routes"`
	Commands     []string      `json:"commands"`
	Migrations   []string      `json:"migrations"`
	// Whether uninstalling can take its tables with it.
	Purges bool `json:"purges"`
}

type SettingInfo struct {
	Key     string `json:"key"`
	Default string `json:"default"`
	Secret  bool   `json:"secret,omitempty"`
}

type RouteInfo struct {
	Pattern    string `json:"pattern"`
	Permission string `json:"permission"`
}

// List is every compiled-in plugin, then every installed package, then any
// row for one this build lacks.
func (m *Manager) List() []Info {
	states := *m.states.Load()
	pkgs := *m.pkgs.Load()
	out := []Info{}
	for _, p := range All() {
		out = append(out, m.info(p, states[p.Name()]))
	}
	names := make([]string, 0, len(pkgs))
	for name := range pkgs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, m.packageInfo(pkgs[name], states[name]))
	}
	var missing []string
	for name, r := range states {
		if _, ok := Lookup(name); ok {
			continue
		}
		if _, isPackage := pkgs[name]; !isPackage && r.State != StateAvailable {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		r := states[name]
		out = append(out, Info{Name: name, State: r.State, Missing: true, Kind: "builtin", Permissions: []string{},
			Manifest: Manifest{Title: Text{EN: name, ZH: name}},
			Installed: InstallRecord{Version: r.Version, At: r.InstalledAt, By: r.InstalledBy,
				UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy}})
	}
	return out
}

// Info is one plugin, compiled in or installed.
func (m *Manager) Info(name string) (Info, error) {
	if l := m.loadedPackage(name); l != nil {
		return m.packageInfo(l, (*m.states.Load())[name]), nil
	}
	p, ok := Lookup(name)
	if !ok {
		return Info{}, ErrUnknown
	}
	return m.info(p, (*m.states.Load())[name]), nil
}

func (m *Manager) info(p Plugin, r record) Info {
	state := r.State
	if state == "" {
		state = StateAvailable
	}
	info := Info{Name: p.Name(), Manifest: p.Manifest(), State: state, Kind: "builtin", Permissions: []string{}}
	if state != StateAvailable {
		info.Installed = InstallRecord{Version: r.Version, At: r.InstalledAt, By: r.InstalledBy,
			UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy}
	}
	info.Contributions = m.contributions(p)
	return info
}

// newContributions is a Contributions with every list empty rather than nil,
// so the backoffice reads [] for a plugin that brought nothing of a kind.
func newContributions() Contributions {
	return Contributions{Settings: []SettingInfo{}, Fields: []string{}, Guards: []string{},
		CaptchaModes: []string{}, AdminRoutes: []RouteInfo{}, PublicRoutes: []string{},
		Commands: []string{}, Migrations: []string{}}
}

// settingInfo is one setting as the backoffice lists it. A secret's default is
// left out: it is a credential the plugin shipped, and the plugin list is not
// where a credential is shown. A compiled-in plugin and a package both list
// their settings through it, so the rule is written once.
func settingInfo(key, def string, secret bool) SettingInfo {
	info := SettingInfo{Key: key, Secret: secret}
	if !secret {
		info.Default = def
	}
	return info
}

func (m *Manager) contributions(p Plugin) Contributions {
	name := p.Name()
	c := newContributions()
	for _, d := range settings.DefinitionsOf(name) {
		c.Settings = append(c.Settings, settingInfo(d.Key, d.Default, d.Secret))
	}
	for _, f := range user.Fields() {
		if f.Plugin == name {
			c.Fields = append(c.Fields, f.Key)
		}
	}
	for mode, owner := range settings.CaptchaModes() {
		if owner == name {
			c.CaptchaModes = append(c.CaptchaModes, mode)
		}
	}
	sort.Strings(c.CaptchaModes)
	if dir := migrationsOf(p); dir != nil {
		// Listed for the screen only; a set that does not read is logged rather
		// than hiding the rest of the plugin's contributions.
		if versions, err := database.Versions(dir); err != nil {
			slog.Warn("plugin: migrations could not be listed for the screen", "plugin", p.Name(), "error", err)
		} else {
			c.Migrations = versions
		}
	}
	c.Purges = purgeOf(p) != nil
	c.Commands = append(c.Commands, console.CommandsOf(name)...)
	if h := m.host; h != nil {
		for _, route := range h.publicRoutes(name) {
			c.PublicRoutes = append(c.PublicRoutes, route.Pattern)
		}
		if h.Auth != nil {
			for _, g := range h.Auth.GuardsOf(name) {
				c.Guards = append(c.Guards, g.Action+":"+g.Name)
			}
		}
		if h.Admin != nil {
			for _, route := range h.Admin.PluginRoutes(name) {
				c.AdminRoutes = append(c.AdminRoutes, RouteInfo{Pattern: route.Pattern, Permission: route.Permission})
			}
		}
	}
	return c
}
