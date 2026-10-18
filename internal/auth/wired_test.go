package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// A Service with no invite hooks is a valid value, so only the wiring check
// stands between a forgotten hook and an invite flow that fails with no error
// anywhere: the check has to name each one it is missing.
func TestAServiceWithoutItsInviteHooksDoesNotBoot(t *testing.T) {
	err := (&Service{}).CheckWired()
	if err == nil {
		t.Fatal("a service with no invite hooks passed the wiring check")
	}
	for _, name := range []string{"ConsumeInvite", "RecordInviteUse", "RewardInvite"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s is not named in %q", name, err)
		}
	}
}

func TestAServiceWiredWithItsInviteHooksPasses(t *testing.T) {
	s := &Service{Hooks: Hooks{
		ConsumeInvite:   func(context.Context, *database.Tx, string) (*InviteGrant, error) { return nil, nil },
		RecordInviteUse: func(context.Context, *database.Tx, InviteGrant, string) error { return nil },
		RewardInvite:    func(context.Context, string, bool) {},
	}}
	if err := s.CheckWired(); err != nil {
		t.Fatalf("a wired service was refused: %v", err)
	}
}
