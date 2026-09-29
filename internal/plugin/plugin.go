// Package plugin is how a feature only some instances want reaches the server
// without the core knowing it exists.
//
// A plugin is a Go package under plugins/ that calls Register from its init.
// It is compiled in or left out by a build tag — cmd/server has one file per
// plugin, each importing it for that side effect — so an instance that does
// not want one carries none of its code, none of its routes and none of its
// tables, and the binary is still the one file the deployment notes promise.
// There is no loading at runtime: Go's own plugin package is Linux-only and
// needs the host and the plugin built by the same toolchain from the same
// module versions, which is a deployment problem dressed as a feature.
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
	// frontend's key for the plugin's own code, and the prefix of its own
	// migrations. It never changes once shipped.
	Name() string
	// Setup attaches the plugin to a server being assembled. It runs once,
	// after the core modules exist and before any route table is mounted,
	// so everything it adds is in place when the first request arrives.
	Setup(*Host) error
}

// Migrator is a plugin that brings tables or columns of its own. The
// filesystem holds *.sql files at its root, run by the same runner and under
// the same rules as the core's (see database.Migrate).
type Migrator interface {
	Migrations() fs.FS
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

// Migrations collects every compiled-in plugin's migration directory.
func Migrations() []fs.FS {
	var out []fs.FS
	for _, p := range All() {
		if m, ok := p.(Migrator); ok {
			if dir := m.Migrations(); dir != nil {
				out = append(out, dir)
			}
		}
	}
	return out
}

// SetupAll runs every plugin's Setup against one host, stopping at the first
// failure: a server with half a plugin attached is worse than one that
// refuses to start and says which.
func SetupAll(h *Host) error {
	for _, p := range All() {
		if err := p.Setup(h); err != nil {
			return fmt.Errorf("plugin %s: %w", p.Name(), err)
		}
	}
	return nil
}
