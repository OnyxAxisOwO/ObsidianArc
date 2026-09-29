//go:build plugin_riskcontrol

package main

// Compiled in by the plugin_riskcontrol build tag — see the PLUGINS variable in the
// Makefile. The import is the whole of it: the plugin registers itself from
// its own init.
import _ "github.com/OnyxAxisOwO/ObsidianArc/plugins/riskcontrol"
