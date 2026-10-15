package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// A destructive command resolves a name to an exact match only. With
// OpenAI-EU as the only provider, "OpenAI" is a substring of it, and a delete
// must not remove OpenAI-EU and every model under it.
func TestADeleteNamedByAPartialProviderNameIsRefused(t *testing.T) {
	providerID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			`{"providers":[{"id":"` + providerID + `","name":"OpenAI-EU"}],"groups":[],"models":[]}`},
		flagRoute{"GET", "/api/admin/providers", 200,
			`{"providers":[{"id":"` + providerID + `","name":"OpenAI-EU"}]}`},
		flagRoute{"DELETE", "/api/admin/providers/" + providerID, 200, `{"ok":true}`},
	)
	result, out := rig.run("provider delete OpenAI --yes")
	if result.OK {
		t.Fatalf("provider delete OpenAI removed a provider picked by a partial name: %s", out)
	}
	if !strings.Contains(out, "exact") {
		t.Errorf("the refusal does not say a change needs the exact name: %q", out)
	}
	for _, call := range rig.calls {
		if strings.HasPrefix(call, "DELETE ") {
			t.Errorf("the provider was deleted: %v", rig.calls)
		}
	}
}
