package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// --generate used to win over --password without a word, so the operator was
// told a generated password and the one they typed was never set. The pair is
// refused before the account is looked up, as mail set refuses its own pair.
func TestUserPasswdRefusesPasswordAndGenerateTogether(t *testing.T) {
	aliceID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200, `{"users":[{"id":"` + aliceID + `","username":"alice"}],"total":1}`},
		flagRoute{"POST", "/api/admin/users/" + aliceID + "/password", 200, `{}`},
	)
	result, out := rig.run("user passwd alice --password typed-password --generate --yes")
	if result.OK {
		t.Fatalf("user passwd accepted both --password and --generate: %s", out)
	}
	if !strings.Contains(out, "not both") {
		t.Errorf("the refusal does not say the flags exclude each other:\n%s", out)
	}
	if len(rig.calls) != 0 {
		t.Errorf("the refused line still sent requests: %v", rig.calls)
	}
}

// --generate false is a switch given off, not a generate request, so it still
// sets the password that was typed.
func TestUserPasswdGenerateFalseWithAPasswordStillSetsIt(t *testing.T) {
	aliceID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200, `{"users":[{"id":"` + aliceID + `","username":"alice"}],"total":1}`},
		flagRoute{"POST", "/api/admin/users/" + aliceID + "/password", 200, `{}`},
	)
	if result, out := rig.run("user passwd alice --password typed-password --generate false --yes"); !result.OK {
		t.Fatalf("user passwd failed: %s", out)
	}
	if got := rig.body(t, "POST /api/admin/users/"+aliceID+"/password")["new_password"]; got != "typed-password" {
		t.Fatalf("new_password = %v, want the typed one", got)
	}
}
