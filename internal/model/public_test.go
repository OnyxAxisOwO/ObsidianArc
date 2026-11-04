package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
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
		APIName:      "gpt-oss",
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

	encoded, err := json.Marshal(toPublic(record, Liveness{}, 1))
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
	for _, shown := range []string{`"api_name":"gpt-oss"`, "GPT OSS 120B", "Fast, and free to use here.", "快速"} {
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

	quiet, err := json.Marshal(toPublic(record, Liveness{}, 1))
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
	warned, err := json.Marshal(toPublic(record, Liveness{Unstable: true}, 1))
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
	published, err := json.Marshal(toPublic(record, Liveness{Uptime: &share}, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(published), `"uptime":0.973`) {
		t.Errorf("the figure did not travel: %s", published)
	}
}

func TestPickerPublishesTheAPINameForTheReadersUsableModels(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	upstream := f.provider(t, "Primary")
	fallback := f.provider(t, "Fallback")
	first := f.model(t, upstream.ID, "shared-model")
	second := f.model(t, fallback.ID, "shared-model")
	solo := f.model(t, upstream.ID, "solo-model")
	named, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "private-upstream", APIName: "gpt-6-sol", DisplayName: "Sol", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	readers, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Readers"})
	if err != nil {
		t.Fatal(err)
	}
	grants := []GroupGrant{
		{ModelID: first.ID, Access: AccessUse}, {ModelID: second.ID, Access: AccessUse},
		{ModelID: solo.ID, Access: AccessUse}, {ModelID: named.ID, Access: AccessUse},
	}
	mux := http.NewServeMux()
	NewHandlers(f.models).Routes(mux)
	list := func() map[string]Public {
		t.Helper()
		if err := f.models.SetGroupModels(ctx, nil, readers.ID, grants); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
		req = req.WithContext(auth.WithUser(req.Context(), user.User{GroupID: readers.ID, Role: user.RoleUser}))
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("list: %d %s", res.Code, res.Body.String())
		}
		var body struct {
			Models []Public `json:"models"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		result := make(map[string]Public)
		for _, model := range body.Models {
			result[model.ID] = model
		}
		return result
	}
	shown := list()
	for id, want := range map[string]string{
		first.ID: "shared-model-" + IDTail(first.ID), second.ID: "shared-model-" + IDTail(second.ID),
		solo.ID: "solo-model", named.ID: "gpt-6-sol",
	} {
		if got := shown[id].APIName; got != want {
			t.Errorf("model %s API name = %q, want %q", id, got, want)
		}
	}
	grants[1].Access = AccessView
	shown = list()
	if got := shown[first.ID].APIName; got != "shared-model" {
		t.Errorf("view-only duplicate qualified the usable model: %q", got)
	}
	if shown[second.ID].Usable {
		t.Error("view-only model became usable")
	}
}
