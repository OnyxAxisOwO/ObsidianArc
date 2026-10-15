package console

import (
	"fmt"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// A blank reference names nothing. Every field of every row contains the empty
// string, so before the guard the only provider on the instance answered for
// "" and a delete with an unset variable removed it.
func TestABlankReferenceNeverResolvesToTheOnlyRow(t *testing.T) {
	providerID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			fmt.Sprintf(`{"providers":[{"id":%q,"name":"Acme"}],"groups":[],"models":[]}`, providerID)},
		flagRoute{"DELETE", "/api/admin/providers/" + providerID, 200, `{"ok":true}`},
		flagRoute{"PATCH", "/api/admin/providers/" + providerID, 200, `{"ok":true}`},
	)
	for _, line := range []string{
		`provider delete "" --yes`,
		`provider edit "" --enabled false`,
	} {
		result, out := rig.run(line)
		if result.OK {
			t.Errorf("%s acted on the only provider: %s", line, out)
		}
		for _, call := range rig.calls {
			if strings.HasPrefix(call, "DELETE ") || strings.HasPrefix(call, "PATCH ") {
				t.Errorf("%s reached the server: %v", line, rig.calls)
			}
		}
	}
}

// A change takes only an exact name or an id. With alice gone, alice_2 is the
// only account whose name contains "alice", and "user delete alice --yes" must
// not delete it because it is the only substring match left.
func TestAChangeNamedByAPartialUsernameIsRefused(t *testing.T) {
	partialID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200,
			`{"users":[{"id":"` + partialID + `","username":"alice_2"}],"total":1}`},
		flagRoute{"DELETE", "/api/admin/users/" + partialID, 200, `{"ok":true}`},
		flagRoute{"POST", "/api/admin/users/" + partialID + "/password", 200, `{"ok":true}`},
	)
	for _, line := range []string{
		"user delete alice --yes",
		"user passwd alice --password a-long-enough-password --yes",
	} {
		result, out := rig.run(line)
		if result.OK {
			t.Errorf("%s changed an account picked by a partial name: %s", line, out)
		}
		if !strings.Contains(out, "exact") {
			t.Errorf("%s: the refusal does not say a change needs the exact name: %q", line, out)
		}
		for _, call := range rig.calls {
			if strings.HasPrefix(call, "DELETE ") || strings.Contains(call, "/password") {
				t.Errorf("%s reached the server: %v", line, rig.calls)
			}
		}
	}
}

// The same command with the exact name still changes the account it names,
// so the refusal above is about the guess and not about writes in general.
func TestAChangeNamedExactlyStillReachesTheAccount(t *testing.T) {
	aliceID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200,
			`{"users":[{"id":"` + aliceID + `","username":"alice"},{"id":"` + id.New() + `","username":"alice_2"}],"total":2}`},
		flagRoute{"DELETE", "/api/admin/users/" + aliceID, 200, `{"ok":true}`},
	)
	if result, out := rig.run("user delete alice --yes"); !result.OK {
		t.Fatalf("user delete alice was refused: %s", out)
	}
	if !containsCall(rig.calls, "DELETE /api/admin/users/"+aliceID) {
		t.Fatalf("the exact account was not deleted: %v", rig.calls)
	}
}

// A blank --group on create means no group, as leaving the flag off does. The
// resolver would otherwise match the only group on the instance.
func TestUserCreateWithABlankGroupSendsNoGroup(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/references", 200,
			fmt.Sprintf(`{"groups":[{"id":%q,"name":"Staff"}],"providers":[],"models":[]}`, id.New())},
		flagRoute{"POST", "/api/admin/users", 200, `{"user":{"id":"x","username":"bob","role":"user","status":"active"}}`},
	)
	if result, out := rig.run("user create bob --group ''"); !result.OK {
		t.Fatalf("user create bob --group '' was refused: %s", out)
	}
	if _, ok := rig.body(t, "POST /api/admin/users")["group_id"]; ok {
		t.Fatalf("the account was given a group from a blank name: %v", rig.bodies)
	}
}

// The usual mistake is a password typed as a second word where --password was
// meant. Nothing else refuses it, and the account would be made with no
// password while the operator believed it had one.
func TestUserCreateRefusesAPasswordTypedAsAWord(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"POST", "/api/admin/users", 200, `{"user":{"id":"x","username":"alice","role":"user","status":"active"}}`},
	)
	result, out := rig.run("user create alice hunter22")
	if result.OK {
		t.Fatalf("user create accepted a password as a word: %s", out)
	}
	for _, call := range rig.calls {
		if strings.HasPrefix(call, "POST ") {
			t.Fatalf("an account was created from a line with a stray word: %v", rig.calls)
		}
	}
}
