package settings

import "testing"

// quota and the backup scheduler keep their own state under two dotted keys
// that are not in Defaults. A package that declared one would be handed, and
// could overwrite, the value the core reads back on every request.
func TestAPluginCannotClaimAKeyTheCoreKeepsItsOwnStateIn(t *testing.T) {
	s := serviceWithGate(map[string]bool{})
	for _, key := range []string{"quota.global_reset_at", "system_backup.lock"} {
		defs := []Definition{{Key: key, Default: "1"}}
		if err := s.CheckPluginDefinitions("zeta", defs, nil); err == nil {
			t.Errorf("CheckPluginDefinitions accepted %s", key)
		}
		if err := s.AddPluginDefinitions("zeta", defs, nil); err == nil {
			t.Errorf("AddPluginDefinitions accepted %s", key)
		}
	}
	if _, ok := s.LookupDefinition("quota.global_reset_at"); ok {
		t.Fatal("a refused definition was kept")
	}
}
