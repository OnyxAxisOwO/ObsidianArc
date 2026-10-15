package console

import (
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// refusedWithoutAWrite asserts a line was refused and that no PATCH or POST
// reached the server: a refused edit that still sent its body would have
// changed the record.
func refusedWithoutAWrite(t *testing.T, line string, result Result, out string, calls []string) {
	t.Helper()
	if result.OK {
		t.Errorf("%s was accepted: %s", line, out)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, http.MethodPatch) || strings.HasPrefix(call, http.MethodPut) || strings.HasPrefix(call, http.MethodPost) {
			t.Errorf("%s reached the server: %s", line, call)
		}
	}
}

// An empty --groups on a model must reach the server as an empty list. A nil
// slice is sent as null, which the server reads as "not given", so the command
// reported success and every grant stayed.
func TestAnEmptyGroupsFlagOnAModelClearsItsGrants(t *testing.T) {
	rig := newAuditRig(t, nil)
	if result, out := rig.run("model edit " + id.New() + " --groups ''"); !result.OK {
		t.Fatalf("model edit --groups '' was refused: %s", out)
	}
	grants, ok := lastBody(t, rig)["group_grants"].([]map[string]any)
	if !ok || grants == nil || len(grants) != 0 {
		t.Errorf("group_grants = %#v, want an empty list that clears the grants", lastBody(t, rig)["group_grants"])
	}
}

func TestAnEmptyTiersFlagOnAModelClearsItsTiers(t *testing.T) {
	rig := newAuditRig(t, nil)
	if result, out := rig.run("model edit " + id.New() + " --tiers ''"); !result.OK {
		t.Fatalf("model edit --tiers '' was refused: %s", out)
	}
	tiers, ok := lastBody(t, rig)["reasoning_tiers"].([]map[string]any)
	if !ok || tiers == nil || len(tiers) != 0 {
		t.Errorf("reasoning_tiers = %#v, want an empty list that clears the tiers", lastBody(t, rig)["reasoning_tiers"])
	}
}

// A budget that does not parse used to be stored as 0, which is a tier that
// has no budget at all. The line is refused before anything is sent.
func TestATierBudgetThatIsNotANumberIsRefused(t *testing.T) {
	rig := newAuditRig(t, nil)
	line := "model edit " + id.New() + " --tiers 'high:Thinking:abc'"
	result, out := rig.run(line)
	refusedWithoutAWrite(t, line, result, out, rig.calls)
}

// The budget is the last field, so a name may contain a colon.
func TestATierNameMayContainAColon(t *testing.T) {
	rig := newAuditRig(t, nil)
	if result, out := rig.run("model edit " + id.New() + " --tiers 'low:Low: fast:1024'"); !result.OK {
		t.Fatalf("a tier whose name has a colon was refused: %s", out)
	}
	tiers, ok := lastBody(t, rig)["reasoning_tiers"].([]map[string]any)
	if !ok || len(tiers) != 1 {
		t.Fatalf("reasoning_tiers = %#v, want one tier", lastBody(t, rig)["reasoning_tiers"])
	}
	if tiers[0]["id"] != "low" || tiers[0]["name"] != "Low: fast" || tiers[0]["budget"] != 1024 {
		t.Errorf("tier = %#v, want id low, name %q, budget 1024", tiers[0], "Low: fast")
	}
}

// The colon form is a common HTTP spelling. It used to be skipped, which left
// an empty map that replaced every stored header on the provider.
func TestAHeaderWithoutAnEqualsSignRefusesTheEdit(t *testing.T) {
	rig := newAuditRig(t, nil)
	line := "provider edit " + id.New() + " --headers 'anthropic-beta: tools'"
	result, out := rig.run(line)
	refusedWithoutAWrite(t, line, result, out, rig.calls)
}

func TestAHeaderWithoutAnEqualsSignRefusesTheCreate(t *testing.T) {
	rig := newAuditRig(t, nil)
	line := "provider create --name P --kind openai --base-url https://api.example.com --api-key k --headers 'X-Foo bar'"
	result, out := rig.run(line)
	refusedWithoutAWrite(t, line, result, out, rig.calls)
}

// An explicitly empty --headers is the documented way to clear them, and it
// has to reach the server as an empty object, not as null.
func TestAnEmptyHeadersFlagClearsTheHeaders(t *testing.T) {
	rig := newAuditRig(t, nil)
	if result, out := rig.run("provider edit " + id.New() + " --headers ''"); !result.OK {
		t.Fatalf("provider edit --headers '' was refused: %s", out)
	}
	headers, ok := lastBody(t, rig)["headers"].(map[string]string)
	if !ok || headers == nil || len(headers) != 0 {
		t.Errorf("headers = %#v, want an empty map that clears them", lastBody(t, rig)["headers"])
	}
}
