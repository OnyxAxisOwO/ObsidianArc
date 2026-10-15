package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// The member query is capped at one page. A group with more members than the
// page holds must say so, or the table reads as the whole membership under a
// count above it that is larger.
func TestGroupShowSaysWhenItsMemberTableIsAPage(t *testing.T) {
	groupID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			`{"groups":[{"id":"` + groupID + `","name":"Staff"}],"models":[],"providers":[]}`},
		flagRoute{"GET", "/api/admin/groups", 200,
			`{"groups":[{"id":"` + groupID + `","name":"Staff","members":350}]}`},
		flagRoute{"GET", "/api/admin/member-options", 200,
			`{"users":[{"id":"` + id.New() + `","username":"alice"},{"id":"` + id.New() + `","username":"bob"}],"total":350}`},
	)
	result, out := rig.run("group show Staff")
	if !result.OK {
		t.Fatalf("group show failed: %s", out)
	}
	if want := "showing 2 of 350 members."; !hasLine(out, want) {
		t.Errorf("group show did not say the table is a page; want %q in:\n%s", want, out)
	}
}

// A table that holds every member says nothing extra.
func TestGroupShowSaysNothingWhenTheTableIsComplete(t *testing.T) {
	groupID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			`{"groups":[{"id":"` + groupID + `","name":"Staff"}],"models":[],"providers":[]}`},
		flagRoute{"GET", "/api/admin/groups", 200,
			`{"groups":[{"id":"` + groupID + `","name":"Staff","members":1}]}`},
		flagRoute{"GET", "/api/admin/member-options", 200,
			`{"users":[{"id":"` + id.New() + `","username":"alice"}],"total":1}`},
	)
	result, out := rig.run("group show Staff")
	if !result.OK {
		t.Fatalf("group show failed: %s", out)
	}
	if hasLine(out, "showing") {
		t.Errorf("group show claimed a page for a complete table:\n%s", out)
	}
}

// hasLine reports whether out has a line equal to want.
func hasLine(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if line == want {
			return true
		}
	}
	return false
}
