package console

import (
	"encoding/json"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// runJSON runs a line with the session in JSON mode, which is what --json sets.
func (rig *flagRig) runJSON(line string) (Result, string) {
	rig.calls = nil
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	return runLine(rig.console, actor, line, true)
}

// decodeObject decodes out as one JSON object. A show command that prints the
// list it searched would fail here, because its top level is not the record.
func decodeObject(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	return m
}

// A show command lists a collection to find the record it was asked for, and
// the list is the last response the console holds. Under --json the record is
// the answer, so the list must not come back in its place.
func TestShowWithJSONPrintsTheRecordNotTheList(t *testing.T) {
	providerID, otherID := id.New(), id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			`{"providers":[{"id":"` + providerID + `","name":"OpenAI"},{"id":"` + otherID + `","name":"Other"}]}`},
		flagRoute{"GET", "/api/admin/providers", 200,
			`{"providers":[{"id":"` + providerID + `","name":"OpenAI","kind":"openai","enabled":true},{"id":"` + otherID + `","name":"Other","kind":"openai"}]}`},
	)
	result, out := rig.runJSON("provider show OpenAI --json")
	if !result.OK {
		t.Fatalf("provider show failed: %s", out)
	}
	m := decodeObject(t, out)
	if m["id"] != providerID || m["name"] != "OpenAI" {
		t.Errorf("provider show --json printed %v, want the OpenAI record", m)
	}
	if _, listed := m["providers"]; listed {
		t.Errorf("provider show --json printed the whole list: %s", out)
	}
}

func TestAppShowWithJSONPrintsTheApplication(t *testing.T) {
	appID, otherID := id.New(), id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/applications", 200,
			`{"applications":[{"id":"` + appID + `","name":"Portal","client_id":"portal-client","confidential":true},` +
				`{"id":"` + otherID + `","name":"Other","client_id":"other-client"}]}`},
	)
	result, out := rig.runJSON("app show " + appID + " --json")
	if !result.OK {
		t.Fatalf("app show failed: %s", out)
	}
	m := decodeObject(t, out)
	if m["id"] != appID || m["name"] != "Portal" {
		t.Errorf("app show --json printed %v, want the Portal application", m)
	}
	if _, listed := m["applications"]; listed {
		t.Errorf("app show --json printed the whole list: %s", out)
	}
}

// The settings endpoint answers with every readable setting. The command asked
// for one key, and that key is all --json should carry.
func TestSettingGetWithJSONPrintsOnlyTheKeyAsked(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/settings", 200,
			`{"settings":{"site.name":"Arc","site.tagline":"hello"}}`},
	)
	result, out := rig.runJSON("setting get site.name --json")
	if !result.OK {
		t.Fatalf("setting get failed: %s", out)
	}
	m := decodeObject(t, out)
	if len(m) != 1 || m["site.name"] != "Arc" {
		t.Errorf("setting get --json printed %v, want only site.name", m)
	}
}

func TestGroupShowWithJSONPrintsTheGroup(t *testing.T) {
	groupID, otherID := id.New(), id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			`{"groups":[{"id":"` + groupID + `","name":"Default"},{"id":"` + otherID + `","name":"Trial"}]}`},
		flagRoute{"GET", "/api/admin/groups", 200,
			`{"groups":[{"id":"` + groupID + `","name":"Default","members":3,"allow_terminal":true},{"id":"` + otherID + `","name":"Trial","members":0}]}`},
	)
	result, out := rig.runJSON("group show Default --json")
	if !result.OK {
		t.Fatalf("group show failed: %s", out)
	}
	m := decodeObject(t, out)
	if m["id"] != groupID || m["name"] != "Default" {
		t.Errorf("group show --json printed %v, want the Default group", m)
	}
	if _, listed := m["groups"]; listed {
		t.Errorf("group show --json printed the whole list: %s", out)
	}
}

func TestModelShowWithJSONPrintsTheModel(t *testing.T) {
	modelID, otherID := id.New(), id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			`{"models":[{"id":"` + modelID + `","display_name":"GPT 4o","model_id":"gpt-4o"},{"id":"` + otherID + `","display_name":"Mini","model_id":"gpt-4o-mini"}]}`},
		flagRoute{"GET", "/api/admin/models", 200,
			`{"models":[{"id":"` + modelID + `","display_name":"GPT 4o","model_id":"gpt-4o","context_window":128000},{"id":"` + otherID + `","display_name":"Mini","model_id":"gpt-4o-mini"}]}`},
	)
	result, out := rig.runJSON("model show gpt-4o --json")
	if !result.OK {
		t.Fatalf("model show failed: %s", out)
	}
	m := decodeObject(t, out)
	if m["id"] != modelID || m["model_id"] != "gpt-4o" {
		t.Errorf("model show --json printed %v, want the gpt-4o model", m)
	}
	if _, listed := m["models"]; listed {
		t.Errorf("model show --json printed the whole list: %s", out)
	}
}

// The text view is the one people read, and it must keep its layout: the
// JSON change is only for --json.
func TestShowTextViewIsUnchangedByTheJSONPath(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/settings", 200,
			`{"settings":{"site.name":"Arc"}}`},
	)
	result, out := rig.run("setting get site.name")
	if !result.OK {
		t.Fatalf("setting get failed: %s", out)
	}
	// The key is dimmed in a coloured session, so the expected layout carries
	// its escapes: an exact match is what notices a moved separator or padding.
	const want = "\x1b[2msite.name:\x1b[0m Arc\n"
	if out != want {
		t.Errorf("setting get printed %q, want %q", out, want)
	}
}
