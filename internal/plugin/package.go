package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A package is a plugin that arrived as data — see package arcx — rather than
// as code compiled into the binary. This file is what it is once the server
// has read it: the archive, the backend compiling in the background, and
// what the backend last said about itself.
//
// Everything a package adds reaches the server through the same extension
// points a compiled-in plugin uses (settings, account fields, guards,
// routes, …), which is why the rest of the server neither knows nor cares
// which kind it is talking to. What differs is that these points are
// attached and detached while the server runs, so each one has a way in and
// a way out (see attach and detach in packages.go).

// Where a package came from.
const (
	SourceUpload  = "upload"
	SourceBundled = "bundled"
)

// loaded is one installed package.
type loaded struct {
	name    string
	pkg     *arcx.Package
	backend *wasm.Backend
	source  string
	addedAt int64
	addedBy string

	// What the backend said about itself the last time it was asked — see
	// describe. Read on every request that needs it, replaced whole.
	described atomic.Pointer[described]
	// Why the package could not be attached at boot, if it could not. A
	// faulted package is inert whatever its state says.
	fault atomic.Pointer[string]
}

// described is a backend's answer to "what should the browser and the page's
// policy be told right now".
type described struct {
	Site    map[string]any `json:"site"`
	First   map[string]any `json:"first"`
	Origins []string       `json:"origins"`
}

func (l *loaded) faulted() string {
	if f := l.fault.Load(); f != nil {
		return *f
	}
	return ""
}

func (l *loaded) has(hook string) bool {
	for _, h := range l.pkg.Manifest.Hooks {
		if h == hook {
			return true
		}
	}
	return false
}

// uiURL is where the browser fetches the plugin's module from. The hash is
// what makes a new version a new address, so the file can be cached
// forever.
func (l *loaded) uiURL() string {
	if l.pkg.Manifest.UI == nil {
		return ""
	}
	return "/api/x/" + l.name + "/" + l.pkg.Manifest.UI.Module + "?v=" + l.pkg.SHA256[:12]
}

// packagePlugin presents a package as the Plugin the manager already knows
// how to install, switch and remove: its migrations, its purge, its
// manifest. Setup does nothing — a package is attached by the manager, at
// the moment it is installed or the server boots, not by a Setup at start.
type packagePlugin struct{ l *loaded }

func (p packagePlugin) Name() string { return p.l.name }

func (p packagePlugin) Manifest() Manifest {
	m := p.l.pkg.Manifest
	return Manifest{
		Version:     m.Version,
		Title:       Text{EN: m.Title.EN, ZH: zhOr(m.Title)},
		Description: Text{EN: m.Description.EN, ZH: zhOr(m.Description)},
		Author:      m.Author,
		Homepage:    m.Homepage,
		License:     m.License,
	}
}

func zhOr(t arcx.Text) string {
	if t.ZH != "" {
		return t.ZH
	}
	return t.EN
}

func (packagePlugin) Setup(*Host) error { return nil }

func (p packagePlugin) Migrations() fs.FS {
	if !p.l.pkg.HasMigrations() {
		return nil
	}
	return p.l.pkg.Migrations()
}

func (p packagePlugin) Purge() fs.FS {
	if !p.l.pkg.HasPurge() {
		return nil
	}
	return p.l.pkg.Purge()
}

// Errors an install of a package can end in, beyond the ones a compiled-in
// plugin has.
var (
	ErrNameTaken   = errors.New("plugin: a built-in plugin already has that name")
	ErrUnchanged   = errors.New("plugin: that package is already installed")
	ErrPreflight   = errors.New("plugin: this package cannot be installed here")
	ErrNoBackend   = errors.New("plugin: this plugin has no backend")
	errPluginFault = errors.New("plugin: the plugin is not running")
)

// PreflightError is a package that is well formed and cannot be installed on
// this server: a setting it defines is a core one, a route collides with
// another, its migration would not run on both databases.
type PreflightError struct{ Reason string }

func (e *PreflightError) Error() string { return "plugin: " + e.Reason }
func (e *PreflightError) Is(target error) bool {
	return target == ErrPreflight
}

func preflight(format string, args ...any) error {
	return &PreflightError{Reason: fmt.Sprintf(format, args...)}
}

// definitionsOf turns a manifest's settings into what the settings service
// keeps, with the validators the manifest's enum and pattern describe.
func definitionsOf(m *arcx.Manifest) []settings.Definition {
	out := make([]settings.Definition, 0, len(m.Settings))
	for _, s := range m.Settings {
		def := settings.Definition{
			Key: s.Key, Default: s.Default, Secret: s.Secret, Permission: s.Permission,
			Plugin: m.Name,
		}
		def.Validate = validatorFor(s)
		out = append(out, def)
	}
	return out
}

func validatorFor(s arcx.Setting) func(string) error {
	var re *regexp.Regexp
	if s.Pattern != "" {
		re = regexp.MustCompile(`^(?:` + s.Pattern + `)$`)
	}
	enum := s.Enum
	if re == nil && len(enum) == 0 {
		return nil
	}
	return func(v string) error {
		if len(enum) > 0 {
			for _, allowed := range enum {
				if v == allowed {
					return nil
				}
			}
			return fmt.Errorf("must be one of %v", enum)
		}
		if !re.MatchString(v) {
			return errors.New("is not in the expected format")
		}
		return nil
	}
}

// fieldsOf turns a manifest's account fields into what the accounts store
// keeps.
func fieldsOf(m *arcx.Manifest) []user.Field {
	out := make([]user.Field, 0, len(m.Fields))
	for _, f := range m.Fields {
		field := user.Field{Key: f.Key, Unique: f.Unique, Searchable: f.Searchable, Plugin: m.Name}
		if f.Pattern != "" {
			re := regexp.MustCompile(`^(?:` + f.Pattern + `)$`)
			field.Validate = func(v string) error {
				if !re.MatchString(v) {
					return errors.New("is not in the expected format")
				}
				return nil
			}
		}
		out = append(out, field)
	}
	return out
}

// lintMigrations holds a package's SQL to the rule the core's own is held to
// — no engine-specific SQL — at install, where a problem is an answer for
// the operator and not a failure on the other database later.
func lintMigrations(p *arcx.Package) error {
	for _, dir := range []struct {
		label string
		fsys  fs.FS
		has   bool
	}{{"migrations", p.Migrations(), p.HasMigrations()}, {"purge", p.Purge(), p.HasPurge()}} {
		if !dir.has {
			continue
		}
		entries, err := fs.ReadDir(dir.fsys, ".")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			raw, err := fs.ReadFile(dir.fsys, entry.Name())
			if err != nil {
				return err
			}
			if problems := database.PortabilityProblems(string(raw)); len(problems) > 0 {
				return preflight("%s/%s: %s", dir.label, entry.Name(), problems[0])
			}
		}
	}
	return nil
}

// startBackend loads the package's backend and starts it compiling. A
// package without one has none.
func (m *Manager) startBackend(l *loaded) {
	if l.pkg.Backend == nil {
		return
	}
	l.backend = m.engine().Load(l.name, l.pkg.Backend, m.hostFunc(l))
}

// invoke calls the backend, making sure nothing it left open outlives the
// call.
func (m *Manager) invoke(ctx context.Context, l *loaded, info wasm.CallInfo, kind string, arg, out any, st *callState) error {
	if l.backend == nil {
		return ErrNoBackend
	}
	if l.faulted() != "" {
		return errPluginFault
	}
	info.Plugin = l.name
	if st == nil {
		st = &callState{}
	}
	defer st.finish()
	return l.backend.Invoke(ctx, info, kind, arg, out, st)
}

// refreshDescribe asks the backend what it wants the browser and the page's
// policy to be told, and remembers the answer. It is asked when the plugin is
// switched on and whenever one of its settings changes, never per request.
// An answer that cannot be had leaves the last one standing: better a
// yesterday's origin than a plugin whose sign-up check vanished mid-flight.
func (m *Manager) refreshDescribe(l *loaded) {
	if !l.has(arcx.HookDescribe) {
		l.described.Store(&described{})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var d described
	if err := m.invoke(ctx, l, wasm.CallInfo{}, "describe", nil, &d, nil); err != nil {
		slog.Warn("plugin describe failed", "plugin", l.name, "error", err)
		if l.described.Load() == nil {
			l.described.Store(&described{})
		}
		return
	}
	for key := range d.Site {
		if len(key) > 0 && key[0] == '_' {
			delete(d.Site, key)
		}
	}
	for key := range d.First {
		if len(key) > 0 && key[0] == '_' {
			delete(d.First, key)
		}
	}
	l.described.Store(&d)
}

// jsonObject re-decodes raw into a generic object, for the answers that are
// passed through to a client untouched.
func jsonObject(raw json.RawMessage) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
