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
