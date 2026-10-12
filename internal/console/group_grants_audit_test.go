package console

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// The same null-for-empty trap as user edit --permissions: an empty --grants
// or --models reached the server as null, which is read as "not given", so the
// command reported success and every grant stayed in place.
func TestAnEmptyGrantsFlagClearsTheModelGrants(t *testing.T) {
	rig := newAuditRig(t, nil)
	rig.run("group edit " + id.New() + " --grants ''")
	grants, ok := lastBody(t, rig)["model_grants"].([]map[string]any)
	if !ok || grants == nil || len(grants) != 0 {
		t.Errorf("model_grants = %#v, want an empty list that clears the grants", lastBody(t, rig)["model_grants"])
	}
}

func TestAnEmptyModelsFlagClearsTheModelIDs(t *testing.T) {
	rig := newAuditRig(t, nil)
	rig.run("group edit " + id.New() + " --models ''")
	ids, ok := lastBody(t, rig)["model_ids"].([]string)
	if !ok || ids == nil || len(ids) != 0 {
		t.Errorf("model_ids = %#v, want an empty list that clears the grants", lastBody(t, rig)["model_ids"])
	}
}
