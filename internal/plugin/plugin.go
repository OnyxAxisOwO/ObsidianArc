// Package plugin is how a feature only some instances want reaches the server
// without the core knowing it exists.
//
// A plugin is a Go package under plugins/ that calls Register from its init.
// It is compiled in or left out by a build tag — cmd/server has one file per
// plugin, each importing it for that side effect — so an instance that does
// not want one carries none of its code. There is no loading of code at
// runtime: Go's own plugin package is Linux-only and needs the host and the
// plugin built by the same toolchain from the same module versions, which is
// a deployment problem dressed as a feature.
//
// What is decided at runtime is whether a compiled-in plugin is installed
// and switched on, which an administrator does from the backoffice the way
// a browser's extensions page works (see Manager). Every plugin is set up
// at boot whatever its state; each extension point asks a plugingate.Gate
// before running what a plugin attached, so switching one off takes effect
// on the next request without a restart and without the plugin's own code
// having to check anything.
//
// The dependency runs one way. Plugins import the core; the core imports
// nothing under plugins/. What a plugin can change is exactly what the core
// modules expose as an extension point — a sign-up guard on auth, a field on
// the accounts table, a route in the backoffice — and Host is where those
// are handed over. A plugin that needs a seam nobody offers adds the seam to
// the module, not a reach into its internals.
package plugin

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
)

// Plugin is one optional feature.
type Plugin interface {
	// Name is a short lowercase identifier: the build tag suffix, the
	// frontend's key for the plugin's own code, the owner every extension
	// point records, and the prefix of its own migrations. It never changes
	// once shipped.
	Name() string
	// Manifest is what the backoffice shows about it.
	Manifest() Manifest
	// Setup attaches the plugin to a server being assembled. It runs once,
	// after the core modules exist and before any route table is mounted,
	// whether or not the plugin is installed: what it attaches is inert
	// until it is switched on.
	Setup(*Host) error
}

// Text is one string in both of the interface's languages.
type Text struct {
	EN string `json:"en"`
	ZH string `json:"zh"`
}

// Manifest is a plugin's description of itself. What it contributes — its
// settings, fields, routes, guards, commands and migrations — is not listed
// here: the manager reads that off the extension points, which cannot
// disagree with what the plugin actually did.
type Manifest struct {
	Version     string `json:"version"`
	Title       Text   `json:"title"`
	Description Text   `json:"description"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`
}

// Migrator is a plugin that brings tables or columns of its own. The
// filesystem holds *.sql files at its root, run by the same runner and under
// the same rules as the core's (see database.Migrate) — at boot for an
// installed plugin, and inside the install's own transaction otherwise.
type Migrator interface {
	Migrations() fs.FS
}

// Purger is a Migrator that can take its migrations back: *.sql files, run
// in name order when an administrator uninstalls the plugin and asks for its
// data to go with it. The migrations are then forgotten, so installing it
// again starts from an empty table rather than a record of one.
type Purger interface {
	Purge() fs.FS
}

var (
	registered []Plugin
	nameRE     = regexp.MustCompile(`^[a-z][a-z0-9]{1,31}$`)
)

// Register adds a plugin. Called from the plugin's init; a bad or repeated
// name panics there, which is at startup rather than at the first request.
func Register(p Plugin) {
	name := p.Name()
	if !nameRE.MatchString(name) {
		panic(fmt.Sprintf("plugin: %q is not a valid plugin name", name))
	}
	for _, other := range registered {
		if other.Name() == name {
			panic("plugin: " + name + " is registered twice")
		}
	}
	registered = append(registered, p)
}

// All lists the compiled-in plugins by name, so setup order does not depend
// on which order the linker ran their init functions in.
func All() []Plugin {
	out := append([]Plugin(nil), registered...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names lists the compiled-in plugins' names, sorted — what the browser is
// told so it knows which of its own plugin modules to fetch.
func Names() []string {
	out := make([]string, 0, len(registered))
	for _, p := range All() {
		out = append(out, p.Name())
	}
	return out
}

// Lookup is the compiled-in plugin called name.
func Lookup(name string) (Plugin, bool) {
	for _, p := range registered {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// Migrations collects every compiled-in plugin's migration directory — for
// restoring an archive, which may carry any of their tables. A running
// server migrates only the installed ones (see Manager.Load).
func Migrations() []fs.FS {
	var out []fs.FS
	for _, p := range All() {
		if dir := migrationsOf(p); dir != nil {
			out = append(out, dir)
		}
	}
	return out
}

func migrationsOf(p Plugin) fs.FS {
	if m, ok := p.(Migrator); ok {
		return m.Migrations()
	}
	return nil
}

func purgeOf(p Plugin) fs.FS {
	if m, ok := p.(Purger); ok {
		return m.Purge()
	}
	return nil
}

// SetupAll runs every plugin's Setup, each against its own view of base —
// the same modules, with the plugin's name and gate — stopping at the first
// failure: a server with half a plugin attached is worse than one that
// refuses to start and says which.
func SetupAll(base *Host) error {
	base.share()
	for _, p := range All() {
		h := *base
		h.name = p.Name()
		if err := p.Setup(&h); err != nil {
			return fmt.Errorf("plugin %s: %w", p.Name(), err)
		}
	}
	return nil
}
