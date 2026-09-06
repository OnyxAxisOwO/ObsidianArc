package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// What a regular user is told about a model must not identify the upstream
// that serves it.
//
// It checks the encoded body for the values rather than asserting on field
// names, because the way this broke was not a field called provider_name
// being read on purpose — it was three screens printing a field that was
// there. A field added later carrying the same string fails here too.
func TestPublicModelDoesNotNameTheUpstream(t *testing.T) {
	record := Model{
		ID:           "01JMODELROWID",
		ModelID:      "openai/gpt-oss-120b",
		DisplayName:  "GPT OSS 120B",
		Description:  "Fast, and free to use here.",
		ProviderID:   "01JPROVIDERROW",
		ProviderName: "Groq",
		ProviderKind: adapter.KindOpenAI,
		Usable:       true,
		ReasoningTiers: []ReasoningTier{
			{ID: "low", Name: "快速", Budget: 1500},
		},
	}

	encoded, err := json.Marshal(toPublic(record, Liveness{}))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body := string(encoded)

	for _, upstream := range []string{"Groq", "openai/gpt-oss-120b", "01JPROVIDERROW", string(adapter.KindOpenAI)} {
		if strings.Contains(body, upstream) {
			t.Errorf("the public model carries %q: %s", upstream, body)
		}
	}

	// The other half of the claim: it still says everything the picker needs,
	// so the fix cannot be "return less and call it private".
	for _, shown := range []string{"GPT OSS 120B", "Fast, and free to use here.", "快速"} {
		if !strings.Contains(body, shown) {
			t.Errorf("the public model lost %q: %s", shown, body)
		}
	}
}

// Silence by default. An availability figure is the operator's own record of
// their instance, and it reaches a reader only where they have said so — a
// field that is absent from the JSON cannot be read off it by accident.
func TestLivenessIsAbsentUnlessTheOperatorPublishedIt(t *testing.T) {
	record := Model{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", DisplayName: "One"}

	quiet, err := json.Marshal(toPublic(record, Liveness{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"uptime", "unstable"} {
		if strings.Contains(string(quiet), field) {
			t.Errorf("%q appeared with nothing to publish: %s", field, quiet)
		}
	}

	// A warning without a number, which is the more common thing to want:
	// readers are told the model is shaky, not how shaky.
	warned, err := json.Marshal(toPublic(record, Liveness{Unstable: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(warned), `"unstable":true`) {
		t.Errorf("the warning did not travel: %s", warned)
	}
	if strings.Contains(string(warned), "uptime") {
		t.Errorf("a warning leaked the figure: %s", warned)
	}

	share := 0.973
	published, err := json.Marshal(toPublic(record, Liveness{Uptime: &share}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(published), `"uptime":0.973`) {
		t.Errorf("the figure did not travel: %s", published)
	}
}
