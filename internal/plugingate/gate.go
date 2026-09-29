// Package plugingate is the one question every extension point asks before it
// runs something a plugin attached: is that plugin switched on right now?
//
// It is its own leaf package because the modules that ask (settings, user,
// auth, admin, …) sit below internal/plugin, which imports all of them. The
// answer is per server rather than per process: every module holds the gate
// it was handed, so two servers built in one test binary can disagree about
// the same plugin without either seeing the other's state.
package plugingate

// Gate reports whether the named plugin is enabled. The core is owner "" and
// is always on; a nil Gate — a module built without a plugin manager, as
// most tests build them — lets everything through, which is what those
// modules did before plugins could be switched off.
type Gate func(plugin string) bool

// Allows is the check itself.
func (g Gate) Allows(plugin string) bool {
	return plugin == "" || g == nil || g(plugin)
}
