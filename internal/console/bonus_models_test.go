package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// A bar stores the internal model ids it covers. A name sent as given matches
// no model, so the bar would cover nothing and its grants would never be spent.
func TestBonusCreateStoresTheModelIDNotTheName(t *testing.T) {
	modelID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			`{"models":[{"id":"` + modelID + `","display_name":"GPT 4o","model_id":"gpt-4o"}],"groups":[],"providers":[]}`},
		flagRoute{"POST", "/api/admin/bonus/bars", 200, `{"bar":{"id":"b1","name":"Welcome"}}`},
	)
	if result, out := rig.run("bonus create Welcome --models gpt-4o"); !result.OK {
		t.Fatalf("bonus create --models gpt-4o was refused: %s", out)
	}
	ids, ok := rig.body(t, "POST /api/admin/bonus/bars")["model_ids"].([]string)
	if !ok || len(ids) != 1 || ids[0] != modelID {
		t.Errorf("model_ids = %#v, want the model's id %q", rig.bodies["POST /api/admin/bonus/bars"], modelID)
	}
}

// A model name that matches nothing is refused, not stored as a bar that
// covers no model.
func TestBonusCreateRefusesAModelThatDoesNotExist(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200, `{"models":[],"groups":[],"providers":[]}`},
		flagRoute{"GET", "/api/admin/models", 200, `{"models":[]}`},
		flagRoute{"POST", "/api/admin/bonus/bars", 200, `{"bar":{"id":"b1","name":"Welcome"}}`},
	)
	result, out := rig.run("bonus create Welcome --models no-such-model")
	if result.OK {
		t.Fatalf("a bar was created for a model that does not exist: %s", out)
	}
	for _, call := range rig.calls {
		if strings.HasPrefix(call, "POST ") {
			t.Errorf("the bar was sent: %s", call)
		}
	}
}

// --group names a group the same way the group commands do. The server's
// reference list answers the lookup, so the grant carries the group's id.
func TestBonusGrantResolvesTheGroupName(t *testing.T) {
	barID, groupID := id.New(), id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/bonus/bars", 200, `{"bars":[{"id":"` + barID + `","name":"Welcome"}]}`},
		flagRoute{"GET", "/api/admin/references", 200,
			`{"groups":[{"id":"` + groupID + `","name":"Trial"}],"models":[],"providers":[]}`},
		flagRoute{"POST", "/api/admin/bonus/bars/" + barID + "/grants", 200, `{"granted":1,"amount":5,"expires_at":0}`},
	)
	if result, out := rig.run("bonus grant " + barID + " --amount 5 --group Trial --yes"); !result.OK {
		t.Fatalf("bonus grant --group Trial was refused: %s", out)
	}
	body := rig.body(t, "POST /api/admin/bonus/bars/"+barID+"/grants")
	if body["group_id"] != groupID {
		t.Errorf("group_id = %v, want the group's id %q", body["group_id"], groupID)
	}
}

// A caller with usage but not the models grant gets the model list refused.
// The refusal names what is missing and says to give the id, rather than
// passing the list's raw 403 on as if the name had been looked up.
func TestBonusCreateSaysANameNeedsTheModelsGrant(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200, `{"groups":[],"providers":[]}`},
		flagRoute{"GET", "/api/admin/models", 403, `{"error":{"code":"forbidden","message":"You do not have access to this."}}`},
		flagRoute{"POST", "/api/admin/bonus/bars", 200, `{"bar":{"id":"b1","name":"Welcome"}}`},
	)
	result, out := rig.run("bonus create Welcome --models gpt-4o")
	if result.OK {
		t.Fatalf("a bar was created from a model name that could not be looked up: %s", out)
	}
	if !strings.Contains(out, "give the model id") {
		t.Errorf("the refusal does not say to give the model id: %s", out)
	}
	for _, call := range rig.calls {
		if strings.HasPrefix(call, "POST ") {
			t.Errorf("the bar was sent: %s", call)
		}
	}
}
