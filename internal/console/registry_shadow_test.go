package console

import (
	"context"
	"testing"
)

// match takes the longest name a line starts with, so a package command whose
// name extends a core command's would run in its place, under the package's
// own grant. The core's "user list" needs users; a package's "user list
// export" with only plugins would otherwise be the one that ran.
func TestAPluginCommandMayNotTakeTheWordsOfAnotherOwnersCommand(t *testing.T) {
	reg := newRegistry(nil)
	run := func(context.Context, *Runtime) error { return nil }

	for _, name := range []string{"user list export", "user list", "user"} {
		if err := reg.add("pkg", []Command{{Name: name, Run: run, Permission: "plugins"}}); err == nil {
			t.Fatalf("a package registered %q, which shares words with a core command", name)
		}
	}
	if cmd, ok := reg.lookup("user list"); !ok || cmd.Plugin != "" {
		t.Fatalf("the core's user list no longer answers to its own name: %+v", cmd)
	}

	if err := reg.add("pkg", []Command{{Name: "pkg thing", Run: run}}); err != nil {
		t.Fatalf("a package's own name: %v", err)
	}
	if err := reg.add("pkg", []Command{{Name: "pkg thing", Run: run}, {Name: "pkg thing extra", Run: run}}); err != nil {
		t.Fatalf("a package updating its own names: %v", err)
	}
	if err := reg.add("other", []Command{{Name: "pkg thing extra", Run: run}}); err == nil {
		t.Fatal("a second package took a name the first one holds")
	}
	if err := reg.add("other", []Command{{Name: "pkg thing extra more", Run: run}}); err == nil {
		t.Fatal("a second package extended a name the first one holds")
	}
}
