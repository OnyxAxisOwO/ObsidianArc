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

	encoded, err := json.Marshal(toPublic(record))
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
