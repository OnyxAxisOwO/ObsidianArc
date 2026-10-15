package server

import (
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// A group and its model grants are one change. A grant that names a model
// which does not exist fails the insert, and that failure must take the group
// with it: the retry of the same form is otherwise refused as a duplicate name.
func TestAGroupWithABadGrantIsNotLeftBehind(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	create := map[string]any{
		"name":         "Trial",
		"model_grants": []map[string]string{{"model_id": id.New(), "access": "use"}},
	}
	if res := in.do(http.MethodPost, "/api/admin/groups", create, founder); res.Code != http.StatusBadRequest || errCode(t, res) != "model_missing" {
		t.Fatalf("a grant for a model that does not exist: %d %s, want 400 model_missing", res.Code, res.Body.String())
	}
	if names := groupNames(t, in, founder); names["Trial"] {
		t.Fatal("the failed create left its group behind")
	}
	if res := in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Trial"}, founder); res.Code != http.StatusCreated {
		t.Fatalf("retrying the same name after a failed create: %d %s", res.Code, res.Body.String())
	}
}

// The same rule for an edit: a rename that cannot write its grants must not
// land either, or the form reports a failure for a change that happened.
func TestAGroupEditWithABadGrantChangesNothing(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	if res := in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Second"}, founder); res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body.String())
	}
	groupID := groupIDs(t, in, founder)["Second"]

	patch := map[string]any{
		"name":         "Renamed",
		"model_grants": []map[string]string{{"model_id": id.New(), "access": "use"}},
	}
	if res := in.do(http.MethodPatch, "/api/admin/groups/"+groupID, patch, founder); res.Code != http.StatusBadRequest || errCode(t, res) != "model_missing" {
		t.Fatalf("a grant for a model that does not exist: %d %s, want 400 model_missing", res.Code, res.Body.String())
	}
	names := groupNames(t, in, founder)
	if !names["Second"] || names["Renamed"] {
		t.Fatalf("the failed edit changed the name: groups %v", names)
	}
}

// groupNames is the set of group names the groups page lists.
func groupNames(t *testing.T, in *instance, as *session) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for name := range groupIDs(t, in, as) {
		out[name] = true
	}
	return out
}

// groupIDs maps each group name the groups page lists to its id.
func groupIDs(t *testing.T, in *instance, as *session) map[string]string {
	t.Helper()
	res := in.do(http.MethodGet, "/api/admin/groups", nil, as)
	if res.Code != http.StatusOK {
		t.Fatalf("list groups: %d %s", res.Code, res.Body.String())
	}
	out := map[string]string{}
	for _, g := range decode[struct {
		Groups []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"groups"`
	}](t, res).Groups {
		out[g.Name] = g.ID
	}
	return out
}
