package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

// Installing, updating and adopting packages: the operations that put a
// package's rows, migrations and seams in place together, or not at all.

// loadPackages reads the installed packages at boot: each archive parsed
// again — what is in the database is checked as carefully as what is
// uploaded — its migrations applied, its backend started compiling. A package
// that cannot be read is left out and logged; one bad row must not be the
// reason the server does not start.
func (m *Manager) loadPackages(ctx context.Context) error {
	rows, err := m.db.Query(ctx,
		`SELECT name, version, sha256, source, archive, added_at, added_by FROM plugin_packages ORDER BY name`)
	if err != nil {
		return fmt.Errorf("plugin: read packages: %w", err)
	}
	defer rows.Close()

	states := *m.states.Load()
	repaired := map[string]record{}
	next := map[string]*loaded{}
	var dirs []fs.FS
	for rows.Next() {
		var name, version, sum, source string
		var archive []byte
		var addedAt int64
		var addedBy string
		if err := rows.Scan(&name, &version, &sum, &source, &archive, &addedAt, &addedBy); err != nil {
			return fmt.Errorf("plugin: scan package: %w", err)
		}
		pkg, err := arcx.Parse(archive)
		if err != nil || pkg.Manifest.Name != name {
			slog.Error("plugin package cannot be read; it stays installed and inert", "plugin", name, "error", err)
			continue
		}
		if _, builtin := Lookup(name); builtin {
			slog.Error("plugin package has the name of a built-in plugin; it stays installed and inert", "plugin", name)
			continue
		}
		l := &loaded{name: name, pkg: pkg, source: source, addedAt: addedAt, addedBy: addedBy}
		next[name] = l
		if r, ok := states[name]; !ok || r.State == "" || r.State == StateAvailable {
			// Installed as a package and recorded nowhere as anything: read
			// as switched off, and written down as such.
			repaired[name] = record{State: StateDisabled, Version: pkg.Manifest.Version, InstalledAt: addedAt, UpdatedAt: addedAt}
		}
		if dir := (packagePlugin{l}).Migrations(); dir != nil {
			dirs = append(dirs, dir)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("plugin: read packages: %w", err)
	}
	rows.Close()

	for name, r := range repaired {
		if _, err := m.db.Exec(ctx,
			`INSERT INTO plugin_installs (name, state, version, installed_at, installed_by, updated_at, updated_by)
			 VALUES (?, ?, ?, ?, '', ?, '')
			 ON CONFLICT (name) DO UPDATE SET state = excluded.state, version = excluded.version, updated_at = excluded.updated_at`,
			name, string(r.State), r.Version, r.InstalledAt, r.UpdatedAt); err != nil {
			return fmt.Errorf("plugin: repair %s: %w", name, err)
		}
		states[name] = r
	}
	if len(dirs) > 0 {
		if _, err := m.db.Migrate(ctx, dirs...); err != nil {
			return err
		}
	}
	m.states.Store(&states)
	m.pkgs.Store(&next)
	for _, l := range next {
		m.startBackend(l)
	}
	return nil
}

// Attach tells the manager about the host the plugins were set up against —
// for the public routes their manifests list, and for the packages, which
// are attached to it now: every module they reach is built, and nothing has
// mounted the backoffice's table yet. A package that cannot be attached is
// left inert and says why; the others carry on.
func (m *Manager) Attach(h *Host) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.host = h
	h.dynamicOrigins = m.packageOrigins
	m.settings.OnChange(m.settingsChanged)
	for _, l := range *m.pkgs.Load() {
		if err := m.attach(l); err != nil {
			slog.Error("plugin package cannot be attached; it stays inert", "plugin", l.name, "error", err)
			l.fault.Store(ptr(err.Error()))
			continue
		}
		if m.State(l.name) == StateEnabled {
			m.refreshDescribe(l)
		}
	}
	if err := m.rebuildRouter(); err != nil {
		slog.Error("plugin routes cannot be served", "error", err)
	}
}

// packageOrigins is every origin the enabled packages ask the page's policy
// to trust right now.
func (m *Manager) packageOrigins() []string {
	var out []string
	for name, l := range *m.pkgs.Load() {
		if !m.Enabled(name) {
			continue
		}
		if d := l.described.Load(); d != nil {
			out = append(out, d.Origins...)
		}
	}
	return out
}

// settingsChanged asks every enabled package that derives something from its
// settings to derive it again, when one of them changed.
func (m *Manager) settingsChanged(keys []string) {
	for name, l := range *m.pkgs.Load() {
		if !l.has(arcx.HookDescribe) || !m.Enabled(name) {
			continue
		}
		if touches(l, keys) {
			m.refreshDescribe(l)
		}
	}
}

func touches(l *loaded, keys []string) bool {
	own := map[string]bool{}
	for _, s := range l.pkg.Manifest.Settings {
		own[s.Key] = true
	}
	for _, key := range keys {
		if own[key] || readableCoreSettings[key] {
			return true
		}
	}
	return false
}

// What a package install did.
type Applied string

const (
	AppliedInstall Applied = "install"
	AppliedUpdate  Applied = "update"
	AppliedAdopt   Applied = "adopt"
)

// InstallPackage installs a package, or updates the one of that name, or
// takes over the plugin an earlier build ran under that name — whichever the
// database says this is — in one transaction: the package's migrations, its
// first settings, its archive, its state, and what it attaches to the running
// server. A failure anywhere leaves everything as it was.
//
// An update keeps the plugin's state, and its settings: what changes is the
// code. Taking over keeps the state too: a plugin that was running as part of
// the binary carries on running as a package.
func (m *Manager) InstallPackage(ctx context.Context, actor Actor, pkg *arcx.Package, source string, opts InstallOptions) (Applied, error) {
	man := pkg.Manifest
	name := man.Name
	if _, builtin := Lookup(name); builtin {
		return "", ErrNameTaken
	}
	if err := lintMigrations(pkg); err != nil {
		return "", err
	}
	values, err := checkAgainst(definitionsOf(man), opts.Settings)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.loadedPackage(name)
	if old != nil && old.pkg.SHA256 == pkg.SHA256 {
		return "", ErrUnchanged
	}
	now := time.Now().UnixMilli()
	nl := &loaded{name: name, pkg: pkg, source: source, addedAt: now, addedBy: actor.ID}
	m.startBackend(nl)
	closeNew := func() { closeLater(nl) }
	if nl.backend != nil {
		if err := nl.backend.Ready(ctx); err != nil {
			closeNew()
			return "", preflight("the backend does not load: %v", err)
		}
	}
	if err := m.checkPackage(pkg, old); err != nil {
		closeNew()
		return "", err
	}

	var (
		op       Applied
		attached bool
		version  = man.Version
	)
	restore := func() {
		if !attached {
			return
		}
		if old != nil {
			m.swapPackages(func(p map[string]*loaded) { p[name] = old })
			if err := m.attach(old); err != nil {
				slog.Error("plugin: could not put the previous version back", "plugin", name, "error", err)
			}
		} else {
			m.detach(nl)
			m.swapPackages(func(p map[string]*loaded) { delete(p, name) })
			_ = m.rebuildRouter()
		}
	}

	var rec record
	err = m.db.Tx(ctx, func(tx *database.Tx) error {
		current, err := m.lock(ctx, tx, name)
		if err != nil {
			return err
		}
		switch {
		case old != nil:
			op = AppliedUpdate
		case current != StateAvailable:
			op = AppliedAdopt
		default:
			op = AppliedInstall
		}
		if pkg.HasMigrations() {
			if _, err := m.db.MigrateIn(ctx, tx, pkg.Migrations()); err != nil {
				return err
			}
		}
		if op == AppliedInstall {
			if err := m.settings.WriteMany(ctx, tx, values); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO plugin_packages (name, version, sha256, source, archive, added_at, added_by)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (name) DO UPDATE SET version = excluded.version, sha256 = excluded.sha256,
			   source = excluded.source, archive = excluded.archive, added_at = excluded.added_at, added_by = excluded.added_by`,
			name, version, pkg.SHA256, source, pkg.Raw, now, actor.ID); err != nil {
			return fmt.Errorf("plugin: store package %s: %w", name, err)
		}
		decision := string(op)
		switch op {
		case AppliedInstall:
			state := StateDisabled
			if opts.Enable {
				state, decision = StateEnabled, "install_enable"
			}
			if _, err := tx.Exec(ctx,
				`UPDATE plugin_installs SET state = ?, version = ?, installed_at = ?, installed_by = ?,
				 updated_at = ?, updated_by = ? WHERE name = ?`,
				string(state), version, now, actor.ID, now, actor.ID, name); err != nil {
				return fmt.Errorf("plugin: install %s: %w", name, err)
			}
			rec = record{State: state, Version: version, InstalledAt: now, InstalledBy: actor.ID, UpdatedAt: now, UpdatedBy: actor.ID}
		default:
			if _, err := tx.Exec(ctx,
				`UPDATE plugin_installs SET version = ?, updated_at = ?, updated_by = ? WHERE name = ?`,
				version, now, actor.ID, name); err != nil {
				return fmt.Errorf("plugin: update %s: %w", name, err)
			}
			rec = (*m.states.Load())[name]
			rec.Version, rec.UpdatedAt, rec.UpdatedBy = version, now, actor.ID
		}

		m.swapPackages(func(p map[string]*loaded) { p[name] = nl })
		attached = true
		if err := m.attach(nl); err != nil {
			return err
		}
		return m.audit(ctx, tx, actor, name, decision)
	})
	if err != nil {
		restore()
		closeNew()
		return "", err
	}

	if op == AppliedInstall {
		m.settings.Remember(values)
	}
	if rec.State == StateEnabled {
		m.refreshDescribe(nl)
	}
	m.set(name, rec)
	closeLater(old)
	return op, nil
}

// checkPackage is everything about a package that depends on this server and
// can be asked without changing it: whether what it defines would collide with
// what is already here.
func (m *Manager) checkPackage(pkg *arcx.Package, old *loaded) error {
	man := pkg.Manifest
	name := man.Name
	if err := m.settings.CheckPluginDefinitions(name, definitionsOf(man), man.CaptchaModes); err != nil {
		return preflight("%v", err)
	}
	if err := m.users.CheckPluginFields(name, fieldsOf(man)); err != nil {
		return preflight("%v", err)
	}
	if m.host != nil {
		for _, b := range man.OAuthBindings {
			if bound := m.host.OAuth.SubjectBindingField(); bound != "" && bound != b.Field {
				return preflight("the %s sign-in's subject is already bound to another account field", b.Provider)
			}
		}
	}
	trial := map[string]*loaded{}
	for k, v := range *m.pkgs.Load() {
		trial[k] = v
	}
	trial[name] = &loaded{name: name, pkg: pkg}
	if _, err := m.buildRouter(trial); err != nil {
		return err
	}
	if m.host != nil && m.host.Mux != nil {
		for _, route := range man.Routes {
			if m.coreClaims(route.Pattern) {
				return preflight("route %q is already served by the server itself", route.Pattern)
			}
		}
	}
	if m.console != nil && len(man.Console) > 0 {
		probe := &loaded{name: name, pkg: pkg}
		if err := m.console.CheckCommands(name, m.commandsOf(probe)); err != nil {
			return preflight("%v", err)
		}
	}
	return nil
}

// coreClaims reports whether a route the server registered itself would
// answer the request the pattern describes. A package route the core
// already serves would never be reached, and pretending otherwise at install
// is worse than saying so.
func (m *Manager) coreClaims(pattern string) bool {
	method, path, _ := strings.Cut(pattern, " ")
	path = wildcardRE.ReplaceAllString(path, "x")
	req, err := http.NewRequest(method, "http://plugin.invalid"+path, nil)
	if err != nil {
		return false
	}
	_, matched := m.host.Mux.Handler(req)
	switch matched {
	case "", "/", "/api/", "/api/admin/":
		return false
	}
	return true
}

// LoadBundled installs, updates or takes over the packages the deployment
// ships with — a directory of archives the server was built with — before the
// first request:
//
//   - a package the instance already runs as a plugin of an earlier build (its
//     state is on record and no package is) is taken over, state and settings
//     and data as they were;
//   - one the operator installed themselves is left alone, and so is one they
//     removed: what they decided outlasts a deploy;
//   - one installed from an earlier deployment is updated to the one shipped now,
//     if that is not older;
//   - one nobody has ever decided about is installed, switched off, for the
//     operator to look at and enable.
func (m *Manager) LoadBundled(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("plugin: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	system := Actor{Username: "bundled"}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".arcx") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("plugin: read %s: %w", entry.Name(), err)
		}
		pkg, err := arcx.Parse(raw)
		if err != nil {
			slog.Error("bundled plugin package is not valid; skipped", "file", entry.Name(), "error", err)
			continue
		}
		name := pkg.Manifest.Name
		if _, builtin := Lookup(name); builtin {
			continue
		}
		existing := m.loadedPackage(name)
		state := (*m.states.Load())[name].State
		switch {
		case existing != nil:
			if existing.source != SourceBundled || existing.pkg.SHA256 == pkg.SHA256 ||
				compareVersions(pkg.Manifest.Version, existing.pkg.Manifest.Version) < 0 {
				continue
			}
		case state == StateAvailable && m.everDecided(name):
			continue
		}
		op, err := m.InstallPackage(ctx, system, pkg, SourceBundled, InstallOptions{})
		if err != nil {
			slog.Error("bundled plugin could not be installed; skipped", "plugin", name, "file", entry.Name(), "error", err)
			continue
		}
		slog.Info("bundled plugin", "plugin", name, "version", pkg.Manifest.Version, "did", string(op))
	}
	return nil
}

// everDecided reports whether the operator has a decision on record for name:
// a row in plugin_installs, which an uninstall leaves as "removed".
func (m *Manager) everDecided(name string) bool {
	var n int
	if err := m.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM plugin_installs WHERE name = ?`, name).Scan(&n); err != nil {
		return true
	}
	return n > 0
}

// compareVersions orders two versions numerically, component by component: 1.10
// is after 1.9. What follows a "-" or "+" is ignored.
func compareVersions(a, b string) int {
	parts := func(v string) []int {
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			v = v[:i]
		}
		var out []int
		for _, p := range strings.Split(v, ".") {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out
	}
	x, y := parts(a), parts(b)
	for len(x) < len(y) {
		x = append(x, 0)
	}
	for len(y) < len(x) {
		y = append(y, 0)
	}
	for i := range x {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// --- what the operator sees before they say yes ------------------------------

// Existing is the installed plugin an upload would replace.
type Existing struct {
	Version string `json:"version"`
	State   State  `json:"state"`
	Source  string `json:"source"`
	SHA256  string `json:"sha256"`
}

// PreviewInfo is what the install dialog shows about an uploaded package —
// everything the manifest asks for and says, before anything has been
// stored or run.
type PreviewInfo struct {
	// What confirms it: the archive is held for a while under this, so the
	// file is uploaded once and what is installed is what was shown.
	Token    string         `json:"token"`
	Manifest *arcx.Manifest `json:"manifest"`
	SHA256   string         `json:"sha256"`
	Size     int            `json:"size"`
	// "install", "update" or "unchanged".
	Action   string    `json:"action"`
	Existing *Existing `json:"existing,omitempty"`
	// Things the operator should know that are not reasons to refuse: a
	// downgrade, a browser half that runs in the page.
	Warnings []string `json:"warnings"`
}

// pendingUploads holds archives between "here is a file" and "yes, install
// it". Small and short-lived: an upload nobody confirms is forgotten.
type pendingUploads struct {
	mu    sync.Mutex
	items map[string]pendingUpload
}

type pendingUpload struct {
	pkg   *arcx.Package
	actor string
	at    time.Time
}

const (
	pendingTTL = 15 * time.Minute
	pendingMax = 8
)

func (p *pendingUploads) put(actor string, pkg *arcx.Package) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.items == nil {
		p.items = map[string]pendingUpload{}
	}
	now := time.Now()
	for token, item := range p.items {
		if now.Sub(item.at) > pendingTTL {
			delete(p.items, token)
		}
	}
	for len(p.items) >= pendingMax {
		var oldest string
		for token, item := range p.items {
			if oldest == "" || item.at.Before(p.items[oldest].at) {
				oldest = token
			}
		}
		delete(p.items, oldest)
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	p.items[token] = pendingUpload{pkg: pkg, actor: actor, at: now}
	return token
}

// take returns the archive held under token if it is the actor's and has not
// expired. It is not consumed: a confirm that fails — a wrong code, a setting
// refused — can be tried again without uploading the file twice.
func (p *pendingUploads) take(actor, token string) (*arcx.Package, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	item, ok := p.items[token]
	if !ok || item.actor != actor || time.Since(item.at) > pendingTTL {
		return nil, false
	}
	return item.pkg, true
}

func (p *pendingUploads) drop(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.items, token)
}

// ErrNoPending is a confirmation whose upload has been forgotten.
var ErrNoPending = errors.New("plugin: that upload has expired; choose the file again")

// Preview reads an uploaded archive and holds it for the actor to confirm.
func (m *Manager) Preview(actor Actor, raw []byte) (*PreviewInfo, error) {
	pkg, err := arcx.Parse(raw)
	if err != nil {
		return nil, err
	}
	man := pkg.Manifest
	if _, builtin := Lookup(man.Name); builtin {
		return nil, ErrNameTaken
	}
	if err := lintMigrations(pkg); err != nil {
		return nil, err
	}
	m.mu.Lock()
	old := m.loadedPackage(man.Name)
	checkErr := m.checkPackage(pkg, old)
	m.mu.Unlock()
	if checkErr != nil {
		return nil, checkErr
	}
	info := &PreviewInfo{Manifest: man, SHA256: pkg.SHA256, Size: len(raw), Action: "install", Warnings: []string{}}
	if man.UI != nil {
		info.Warnings = append(info.Warnings, "ui")
	}
	if old != nil {
		state := m.State(man.Name)
		info.Existing = &Existing{Version: old.pkg.Manifest.Version, State: state, Source: old.source, SHA256: old.pkg.SHA256}
		info.Action = "update"
		switch {
		case old.pkg.SHA256 == pkg.SHA256:
			info.Action = "unchanged"
		case compareVersions(man.Version, old.pkg.Manifest.Version) < 0:
			info.Warnings = append(info.Warnings, "downgrade")
		}
	}
	info.Token = m.pending.put(actor.ID, pkg)
	return info, nil
}

// ConfirmUpload installs the archive the actor previewed under token.
func (m *Manager) ConfirmUpload(ctx context.Context, actor Actor, token string, opts InstallOptions) (string, Applied, error) {
	pkg, ok := m.pending.take(actor.ID, token)
	if !ok {
		return "", "", ErrNoPending
	}
	op, err := m.InstallPackage(ctx, actor, pkg, SourceUpload, opts)
	if err != nil {
		return "", "", err
	}
	m.pending.drop(token)
	return pkg.Manifest.Name, op, nil
}

// packageInfo is one installed package as the backoffice lists it.
func (m *Manager) packageInfo(l *loaded, r record) Info {
	state := r.State
	if state == "" {
		state = StateDisabled
	}
	man := l.pkg.Manifest
	info := Info{
		Name: l.name, Manifest: packagePlugin{l}.Manifest(), State: state, Kind: "package",
		Installed: InstallRecord{Version: r.Version, At: r.InstalledAt, By: r.InstalledBy,
			UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy},
		Permissions: append([]string{}, man.Permissions...),
		SHA256:      l.pkg.SHA256, Size: len(l.pkg.Raw), Source: l.source,
		HasUI: man.UI != nil, Fault: l.faulted(),
	}
	c := Contributions{Settings: []SettingInfo{}, Fields: []string{}, Guards: []string{},
		CaptchaModes: append([]string{}, man.CaptchaModes...), AdminRoutes: []RouteInfo{}, PublicRoutes: []string{},
		Commands: []string{}, Migrations: []string{}, Purges: l.pkg.HasPurge()}
	for _, s := range man.Settings {
		item := SettingInfo{Key: s.Key, Secret: s.Secret}
		if !s.Secret {
			item.Default = s.Default
		}
		c.Settings = append(c.Settings, item)
	}
	for _, f := range man.Fields {
		c.Fields = append(c.Fields, f.Key)
	}
	for _, g := range man.Guards {
		c.Guards = append(c.Guards, g.Action+":"+g.Name)
	}
	for _, route := range man.Routes {
		if route.Access == arcx.AccessAdmin {
			c.AdminRoutes = append(c.AdminRoutes, RouteInfo{Pattern: route.Pattern, Permission: route.Permission})
		} else {
			c.PublicRoutes = append(c.PublicRoutes, route.Pattern)
		}
	}
	for _, cmd := range man.Console {
		c.Commands = append(c.Commands, cmd.Name)
	}
	if dir := (packagePlugin{l}).Migrations(); dir != nil {
		if versions, err := database.Versions(dir); err == nil {
			c.Migrations = versions
		}
	}
	sort.Strings(c.CaptchaModes)
	info.Contributions = c
	return info
}
