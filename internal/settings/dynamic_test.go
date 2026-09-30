package settings

import (
	"errors"
	"testing"
)

// A service whose plugin gate is a table the test edits.
func serviceWithGate(on map[string]bool) *Service {
	s := New(nil)
	s.SetPluginGate(func(plugin string) bool { return on[plugin] })
	return s
}

func TestInstalledDefinitionsAreKnownWhileTheirPluginIsOn(t *testing.T) {
	on := map[string]bool{}
	s := serviceWithGate(on)
	defs := []Definition{
		{Key: "zeta.mode", Default: "open", Validate: func(v string) error {
			if v != "open" && v != "closed" {
				return errors.New("open or closed")
			}
			return nil
		}},
		{Key: "zeta.secret", Secret: true, Permission: "security"},
	}
	if err := s.AddPluginDefinitions("zeta", defs, []string{"zetacheck"}); err != nil {
		t.Fatal(err)
	}

	// Installed and switched off: defined, and known to nobody.
	if _, ok := s.Defined("zeta.mode"); ok || s.Known("zeta.mode") || s.ValidCaptchaMode("zetacheck") {
		t.Fatal("a switched-off plugin's settings are known")
	}
	if d, ok := s.LookupDefinition("zeta.secret"); !ok || !d.Secret || d.Permission != "security" {
		t.Fatalf("LookupDefinition = %+v, %v", d, ok)
	}
	if _, ok := s.All()["zeta.mode"]; ok {
		t.Fatal("All lists a switched-off plugin's key")
	}

	on["zeta"] = true
	d, ok := s.Defined("zeta.mode")
	if !ok || d.Plugin != "zeta" || d.Validate("maybe") == nil {
		t.Fatalf("Defined = %+v, %v", d, ok)
	}
	if got := s.Get("zeta.mode"); got != "open" {
		t.Fatalf("an unset installed setting reads %q, not its default", got)
	}
	if !s.Known("zeta.mode") || !s.ValidCaptchaMode("zetacheck") {
		t.Fatal("an enabled plugin's setting or mode is not known")
	}
	if got := s.All()["zeta.mode"]; got != "open" {
		t.Fatalf("All lists zeta.mode as %q", got)
	}
	if n := len(s.DefinitionsOf("zeta")); n != 2 {
		t.Fatalf("DefinitionsOf = %d", n)
	}
	if owner := s.CaptchaModesOf()["zetacheck"]; owner != "zeta" {
		t.Fatalf("owner of the mode = %q", owner)
	}
	found := false
	for _, d := range s.Definitions() {
		found = found || d.Key == "zeta.mode"
	}
	if !found {
		t.Fatal("Definitions omits an enabled plugin's key")
	}
}

func TestAnInstalledDefinitionIsPerServiceNotPerProcess(t *testing.T) {
	a := serviceWithGate(map[string]bool{"zeta": true})
	b := serviceWithGate(map[string]bool{"zeta": true})
	if err := a.AddPluginDefinitions("zeta", []Definition{{Key: "zeta.mode", Default: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	if b.Known("zeta.mode") {
		t.Fatal("one service's install is visible to another")
	}
}

func TestConflictsAreRefusedWholeAndLeaveNothingBehind(t *testing.T) {
	s := serviceWithGate(map[string]bool{"zeta": true, "eta": true})
	if err := s.AddPluginDefinitions("zeta", []Definition{{Key: "zeta.a"}, {Key: SiteName}}, nil); err == nil {
		t.Fatal("a core key was taken")
	}
	if _, ok := s.LookupDefinition("zeta.a"); ok {
		t.Fatal("a refused call left its first definition behind")
	}
	if err := s.AddPluginDefinitions("zeta", []Definition{{Key: "zeta.a"}}, []string{CaptchaModeTurnstile}); err == nil {
		t.Fatal("a core captcha mode was taken")
	}
	if err := s.AddPluginDefinitions("zeta", []Definition{{Key: "shared.key"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPluginDefinitions("eta", []Definition{{Key: "shared.key"}}, nil); err == nil {
		t.Fatal("two plugins owned one key")
	}
	if err := s.AddPluginDefinitions("zeta", []Definition{{Key: ""}}, nil); err == nil {
		t.Fatal("an empty key was accepted")
	}
	// The same plugin adding again is an update, not a conflict.
	if err := s.AddPluginDefinitions("zeta", []Definition{{Key: "shared.key", Default: "new"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("shared.key"); got != "new" {
		t.Fatalf("an update kept the old default: %q", got)
	}
}

func TestRemovingAPluginsDefinitionsForgetsThemAndKeepsTheirValues(t *testing.T) {
	s := serviceWithGate(map[string]bool{"zeta": true})
	if err := s.AddPluginDefinitions("zeta", []Definition{{Key: "zeta.mode", Default: "open"}}, []string{"zetacheck"}); err != nil {
		t.Fatal(err)
	}
	s.Remember(map[string]string{"zeta.mode": "closed"})
	s.RemovePluginDefinitions("zeta")
	if s.Known("zeta.mode") || s.ValidCaptchaMode("zetacheck") {
		t.Fatal("a removed plugin's settings are still known")
	}
	if !s.Stored([]string{"zeta.mode"}) {
		t.Fatal("removing the definitions dropped the stored value; that is the uninstall's call")
	}
	// Reinstalling finds the value where it was left.
	if err := s.AddPluginDefinitions("zeta", []Definition{{Key: "zeta.mode", Default: "open"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("zeta.mode"); got != "closed" {
		t.Fatalf("after a reinstall zeta.mode reads %q", got)
	}
}

func TestListenersHearWhichKeysChanged(t *testing.T) {
	s := serviceWithGate(nil)
	var heard [][]string
	s.OnChange(func(keys []string) { heard = append(heard, keys) })
	s.Remember(map[string]string{"a.b": "1"})
	s.Forget([]string{"a.b"})
	if len(heard) != 2 || heard[0][0] != "a.b" || heard[1][0] != "a.b" {
		t.Fatalf("heard %v", heard)
	}
}
