package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Deleting a card is permanent and the account is not told, so it asks for
// --yes like every other delete in this package. Without the flag it used to
// run at once.
func TestDroppingACardNeedsYes(t *testing.T) {
	rig := newAuditRig(t, nil)
	result, _ := rig.run("user cards drop " + id.New() + " " + id.New())
	if result.Code != "confirmation_required" {
		t.Errorf("result = %+v, want the confirmation refusal", result)
	}
	if len(rig.calls) != 0 {
		t.Errorf("the card was deleted without --yes: %v", rig.calls)
	}
}

// A new delete command must not be able to forget the flag the way
// `user cards drop` did, so every command that calls DELETE is checked here.
func TestEveryCommandThatDeletesAsksForYes(t *testing.T) {
	for _, cmd := range allCommands {
		for _, endpoint := range cmd.Endpoints {
			if strings.HasPrefix(endpoint, "DELETE ") && !cmd.Destructive {
				t.Errorf("%s calls %s but is not Destructive, so it runs without --yes", cmd.Name, endpoint)
			}
		}
	}
}
