package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// --api-key and --copy-key-from are one choice. With both, the typed key won
// and the copy vanished without a word; with neither, the line went to the
// server only to be refused there. Both are refused before anything is sent.
func TestProviderCreateTakesExactlyOneKeySource(t *testing.T) {
	sourceID := id.New()
	base := "provider create --name P --kind openai --base-url https://api.example.com"
	for _, line := range []string{
		base + " --api-key sk-test --copy-key-from " + sourceID,
		base,
	} {
		rig := newFlagRig(
			flagRoute{"GET", "/api/admin/references", 200, `{"providers":[{"id":"` + sourceID + `","name":"Source"}]}`},
			flagRoute{"POST", "/api/admin/providers", 200, `{"provider":{"id":"` + id.New() + `"}}`},
		)
		result, out := rig.run(line)
		if result.OK {
			t.Errorf("%s was accepted: %s", line, out)
		}
		if !strings.Contains(out, "exactly one") {
			t.Errorf("%s: the refusal does not name the rule: %q", line, out)
		}
		if len(rig.calls) != 0 {
			t.Errorf("%s reached the server: %v", line, rig.calls)
		}
	}
}

// Each source on its own still creates the provider, and the copy names the
// source it was given rather than a key.
func TestProviderCreateWithOneKeySourceSendsIt(t *testing.T) {
	sourceID := id.New()
	base := "provider create --name P --kind openai --base-url https://api.example.com"
	rig := newFlagRig(
		flagRoute{"POST", "/api/admin/providers", 200, `{"provider":{"id":"` + id.New() + `"}}`},
	)
	if result, out := rig.run(base + " --api-key sk-test"); !result.OK {
		t.Fatalf("--api-key alone was refused: %s", out)
	}
	if body := rig.body(t, "POST /api/admin/providers"); body["api_key"] != "sk-test" {
		t.Errorf("api_key = %v, want the typed key", body["api_key"])
	}

	rig = newFlagRig(
		flagRoute{"POST", "/api/admin/providers", 200, `{"provider":{"id":"` + id.New() + `"}}`},
	)
	if result, out := rig.run(base + " --copy-key-from " + sourceID); !result.OK {
		t.Fatalf("--copy-key-from alone was refused: %s", out)
	}
	if body := rig.body(t, "POST /api/admin/providers"); body["copy_key_from"] != sourceID {
		t.Errorf("copy_key_from = %v, want %s", body["copy_key_from"], sourceID)
	}
}
