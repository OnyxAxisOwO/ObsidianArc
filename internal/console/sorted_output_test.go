package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// A map has no order, so the columns that list one must list it sorted, or the
// same provider prints its headers differently each time it is shown.
func TestProviderHeadersAreShownSorted(t *testing.T) {
	provider := id.New()
	body := `{"providers":[{"id":"` + provider + `","name":"acme","kind":"openai",` +
		`"base_url":"https://example.test","headers":{"X-Zeta":"1","X-Alpha":"2","X-Mid":"3"}}]}`
	rig := newStubRig(func(string, string) (int, string) { return 200, body })

	result, out := rig.run("provider show " + provider)
	if !result.OK {
		t.Fatalf("provider show failed:\n%s", out)
	}
	alpha, mid, zeta := strings.Index(out, "X-Alpha=2"), strings.Index(out, "X-Mid=3"), strings.Index(out, "X-Zeta=1")
	if alpha < 0 || mid < 0 || zeta < 0 || !(alpha < mid && mid < zeta) {
		t.Errorf("headers are not listed in order:\n%s", out)
	}
}

// The same holds for the windows a quota policy lists.
func TestQuotaWindowsAreListedSorted(t *testing.T) {
	body := `{"policies":[{"scope":"global","scope_id":"","rpm":null,"tpm":null,` +
		`"windows":{"month":{},"hour":{},"day":{}},"updated_at":0}]}`
	rig := newStubRig(func(string, string) (int, string) { return 200, body })

	result, out := rig.run("quota list")
	if !result.OK {
		t.Fatalf("quota list failed:\n%s", out)
	}
	if !strings.Contains(out, "day,hour,month") {
		t.Errorf("the windows are not listed in order:\n%s", out)
	}
}

// A write with nothing else to show says what happened, in text. Under --json the
// same write answers the empty object, which the JSON contract test checks.
func TestASilentWriteSaysWhatHappenedInText(t *testing.T) {
	rig := newStubRig(func(string, string) (int, string) { return 200, `{"ok":true}` })

	result, out := rig.run("user cards drop " + id.New() + " " + id.New() + " --yes")
	if !result.OK || out != "deleted.\n" {
		t.Errorf("user cards drop: ok=%v out=%q, want the line naming the outcome", result.OK, out)
	}

	result, out = rig.run("credit bonus switch " + id.New() + " on")
	if !result.OK || out != "switched on.\n" {
		t.Errorf("credit bonus switch: ok=%v out=%q, want the line naming the outcome", result.OK, out)
	}
}
