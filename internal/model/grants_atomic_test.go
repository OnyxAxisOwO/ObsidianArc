package model

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// A model and its group grants are one write. Before, the grants were a second
// transaction: a refused grant left a saved model that no group could use, and
// a retry saved a second copy of it.
func TestAModelAndItsGrantsAreWrittenTogether(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	open, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Open", AllowAllModels: true})
	if err != nil {
		t.Fatal(err)
	}
	ghost := "group-that-does-not-exist"

	createWith := func(modelID string, grants []ModelGroupGrant) (Model, error) {
		return f.models.Create(ctx, CreateInput{
			ProviderID:   upstream.ID,
			ModelID:      modelID,
			DisplayName:  modelID,
			Enabled:      true,
			GroupGrants:  &grants,
			Capabilities: Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
			Weights:      Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
		})
	}

	// A grant the database refuses refuses the model with it.
	if _, err := createWith("refused", []ModelGroupGrant{{GroupID: ghost, Access: AccessUse}}); err == nil {
		t.Fatal("a grant to a group that does not exist was accepted")
	}
	var saved int
	_ = f.db.QueryRow(ctx, `SELECT COUNT(*) FROM models WHERE model_id = ?`, "refused").Scan(&saved)
	if saved != 0 {
		t.Fatalf("a refused grant left %d models behind", saved)
	}

	// The model reports what was stored: 'none' is dropped and a repeated
	// group keeps its first grant.
	record, err := createWith("granted", []ModelGroupGrant{
		{GroupID: open.ID, Access: AccessView},
		{GroupID: open.ID, Access: AccessUse},
		{GroupID: ghost, Access: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(record.GroupGrants) != 1 || record.GroupGrants[0] != (ModelGroupGrant{GroupID: open.ID, Access: AccessView}) {
		t.Fatalf("create reported %+v, want the stored grant only", record.GroupGrants)
	}

	// An update that fails on its grants leaves the model as it was.
	name := "renamed"
	bad := []ModelGroupGrant{{GroupID: ghost, Access: AccessUse}}
	if _, err := f.models.Update(ctx, record.ID, Update{DisplayName: &name, GroupGrants: &bad}); err == nil {
		t.Fatal("an update with a grant to a missing group was accepted")
	}
	after, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DisplayName != "granted" {
		t.Errorf("a refused update renamed the model to %q", after.DisplayName)
	}
}
