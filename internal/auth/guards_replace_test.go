package auth

import "testing"

// ReplaceGuards runs while the server serves, so it refuses what AddGuard
// panics on — and refusing has to leave the doors exactly as they were, or a
// failed update would take the working version's guard away with it.
func TestReplaceGuardsRefusesWhatAddGuardWouldAndChangesNothing(t *testing.T) {
	cases := []struct {
		name   string
		plugin string
		added  map[string][]Guard
	}{
		{"no plugin", "", map[string][]Guard{GuardRegister: {{Name: "g", Plugin: ""}}}},
		{"unknown action", "p", map[string][]Guard{"nowhere": {{Name: "g", Plugin: "p"}}}},
		{"ownerless guard", "p", map[string][]Guard{GuardRegister: {{Name: "g"}}}},
		{"another plugin's guard", "p", map[string][]Guard{GuardLogin: {{Name: "g", Plugin: "q"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.auth.AddGuard(GuardRegister, Guard{Name: "old", Plugin: "p"})
			if err := f.auth.ReplaceGuards(c.plugin, c.added); err == nil {
				t.Fatal("ReplaceGuards accepted it")
			}
			got := f.auth.guardsFor(GuardRegister)
			if len(got) != 1 || got[0].Name != "old" {
				t.Fatalf("register guards = %+v, want the old one alone", got)
			}
			if got := f.auth.guardsFor(GuardLogin); len(got) != 0 {
				t.Fatalf("login guards = %+v, want none", got)
			}
		})
	}
}

func TestReplaceGuardsSwapsOnlyThePluginsOwn(t *testing.T) {
	f := newFixture(t)
	f.auth.AddGuard(GuardRegister, Guard{Name: "mine", Plugin: "p"})
	f.auth.AddGuard(GuardRegister, Guard{Name: "theirs", Plugin: "q"})
	if err := f.auth.ReplaceGuards("p", map[string][]Guard{
		GuardLogin: {{Name: "new", Plugin: "p"}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.auth.guardsFor(GuardRegister); len(got) != 1 || got[0].Name != "theirs" {
		t.Fatalf("register guards = %+v, want the other plugin's alone", got)
	}
	if got := f.auth.guardsFor(GuardLogin); len(got) != 1 || got[0].Name != "new" {
		t.Fatalf("login guards = %+v, want the new one", got)
	}
	f.auth.RemoveGuards("p")
	if got := f.auth.guardsFor(GuardLogin); len(got) != 0 {
		t.Fatalf("login guards after removal = %+v", got)
	}
}
