package console

import (
	"fmt"
	"testing"
)

// quota set carries forward every window it read, so it sends back when it read
// them; the server refuses the save if the policy has moved since, rather than
// putting another administrator's window back the way it was.
func TestQuotaSetSendsTheUpdatedAtItRead(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/quota/policies", 200,
			`{"policies":[{"scope":"global","scope_id":"","rpm":30,"tpm":null,"windows":{},"updated_at":1760000000123}]}`},
		flagRoute{"PUT", "/api/admin/quota/policies", 200,
			`{"policy":{"scope":"global","scope_id":"","rpm":40,"tpm":null,"updated_at":1760000000999}}`},
	)
	if result, out := rig.run("quota set --scope global --rpm 40"); !result.OK {
		t.Fatalf("quota set failed: %s", out)
	}
	if got := rig.body(t, "PUT /api/admin/quota/policies")["if_updated_at"]; fmt.Sprint(got) != "1760000000123" {
		t.Fatalf("if_updated_at = %v, want the updated_at that was read", got)
	}
}

// A level with no policy yet is read as absent, and the save says so, so a
// policy somebody else created meanwhile is not silently replaced.
func TestQuotaSetOnANewLevelExpectsNoPolicy(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/quota/policies", 200, `{"policies":[]}`},
		flagRoute{"PUT", "/api/admin/quota/policies", 200,
			`{"policy":{"scope":"global","scope_id":"","rpm":40,"tpm":null,"updated_at":1}}`},
	)
	if result, out := rig.run("quota set --scope global --rpm 40"); !result.OK {
		t.Fatalf("quota set failed: %s", out)
	}
	if got := rig.body(t, "PUT /api/admin/quota/policies")["if_updated_at"]; fmt.Sprint(got) != "0" {
		t.Fatalf("if_updated_at = %v, want 0 for a level with no policy", got)
	}
}
