package plugin

import (
	"context"
	"slices"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

// AttachConsole adds a package's commands whether or not a host is there, and
// the commands close over its backend. A package removed before the host
// arrives must therefore leave the console with them, or the console keeps
// calling a backend the server has already let go.
func TestDetachTakesConsoleCommandsOffWithoutAHost(t *testing.T) {
	c := console.New(console.Options{})
	if err := c.AddCommands("ghost", []console.Command{{
		Name:       "ghost ping",
		Permission: console.Anyone,
		Run:        func(context.Context, *console.Runtime) error { return nil },
	}}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{console: c}
	if m.host != nil {
		t.Fatal("the test needs a manager with no host")
	}

	m.detach(&loaded{name: "ghost"})

	if names := c.CommandNamesOf("ghost"); len(names) != 0 {
		t.Fatalf("commands left registered after detach with no host: %v", names)
	}
}

// A failed update is put back by restoreInstall, which detaches the new version
// and attaches the old one. With no host attached, that attach must still
// re-add the old version's console commands, or a rolled-back update leaves the
// console without the commands the operator had.
func TestRestoringAnUpdateWithNoHostPutsItsConsoleCommandsBack(t *testing.T) {
	r := newRig(t)
	c := console.New(console.Options{})
	r.manager.console = c
	old := ghostWithCommands("1.0.0", "ghost ping")
	next := ghostWithCommands("1.1.0", "ghost pong")

	if err := r.manager.attach(next); err != nil {
		t.Fatal(err)
	}
	r.manager.restoreInstall("ghost", old, next)

	if names := c.CommandNamesOf("ghost"); !slices.Equal(names, []string{"ghost ping"}) {
		t.Fatalf("after the rollback the console has %v, not the old version's commands", names)
	}
}

func ghostWithCommands(version string, commands ...string) *loaded {
	man := &arcx.Manifest{Name: "ghost", Version: version}
	for _, name := range commands {
		man.Console = append(man.Console, arcx.ConsoleCommand{Name: name, Summary: arcx.Text{EN: "ghost command"}})
	}
	return &loaded{name: "ghost", pkg: &arcx.Package{Manifest: man}}
}
