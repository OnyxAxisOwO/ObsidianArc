package console

import (
	"strings"
	"testing"
)

const cappedAllowances = `{"rows":[],"capped":true,"listed":1000000,"total":2000000}`

// Under --json the response already says capped, listed and total, so the
// sentence that explains a partial listing must not go in front of the array.
func TestCappedAllowancesUnderJSONIsOneDocument(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/admin/usage/allowances", 200, cappedAllowances})
	result, out := rig.runJSON("usage allowances --json")
	if !result.OK {
		t.Fatalf("usage allowances --json failed: %s", out)
	}
	if capped, _ := decodeObject(t, out)["capped"].(bool); !capped {
		t.Errorf("the capped flag is missing from the JSON document:\n%s", out)
	}
}

// The counts are printed as plain numbers. A float printed with %v becomes
// 1e+06 at a million, which is what a sentence about a large instance hits first.
func TestCappedAllowancesNoteWritesTheCountsInFull(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/admin/usage/allowances", 200, cappedAllowances})
	result, out := rig.run("usage allowances")
	if !result.OK {
		t.Fatalf("usage allowances failed: %s", out)
	}
	if strings.Contains(out, "e+") {
		t.Errorf("a count was printed in exponent form:\n%s", out)
	}
	if !strings.Contains(out, "newest 1000000 of 2000000 accounts") {
		t.Errorf("the capped note is missing or misformatted:\n%s", out)
	}
}

const cappedMatching = `{"rows":[],"capped":true,"listed":3,"total":3,"loaded":5000}`

// A state filter past the cap is counted over the loaded accounts, so the note
// must say that. Worded as "the newest 3 of 3" it reads as a complete population.
func TestCappedAllowancesWithAStateSaysTheMatchesAreAmongTheLoaded(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/admin/usage/allowances", 200, cappedMatching})
	result, out := rig.run("usage allowances --state exhausted")
	if !result.OK {
		t.Fatalf("usage allowances --state exhausted failed: %s", out)
	}
	if !strings.Contains(out, "3 matching accounts among the newest 5000") {
		t.Errorf("the filtered capped note is missing or misformatted:\n%s", out)
	}
	if strings.Contains(out, "Showing the newest") {
		t.Errorf("the filtered count is worded as the whole population:\n%s", out)
	}
}
