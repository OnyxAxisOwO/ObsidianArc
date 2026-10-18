package server

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
)

// A plugin guard's refusal at the sign-up door is filed under the plugin. With
// no source it read as the core's own entry, which is what the guard against a
// package forging the core's events exists to prevent.
func TestAPluginGuardsRefusalIsFiledUnderThePlugin(t *testing.T) {
	in := newInstance(t)
	ctx := context.Background()
	// The first account is never challenged, so the door is set up after it.
	if _, _, err := in.server.auth.Register(ctx, auth.RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	// The build carries no plugin, so the server's own gate would switch the
	// guard off before it ran; this one lets it stand at the door.
	in.server.auth.SetPluginGate(func(string) bool { return true })
	in.server.auth.AddGuard(auth.GuardRegister, auth.Guard{
		Name: "risk", Plugin: "riskctl", Event: "risk_block",
		Check: func(context.Context, auth.GuardRequest) (auth.Verdict, error) {
			return auth.Verdict{}, &auth.GuardRefusal{Err: errors.New("refused"), Reason: "looks automated"}
		},
	})

	if _, _, err := in.server.auth.Register(ctx, auth.RegisterInput{
		Username: "visitor", Password: "a-good-password", IP: "203.0.113.7",
	}); err == nil {
		t.Fatal("the guard's refusal let the sign-up through")
	}

	events, _, err := securityevents.NewStore(in.db).List(ctx, securityevents.Filter{Event: "risk_block", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want the one refusal", events)
	}
	if events[0].Source != "plugin:riskctl" {
		t.Fatalf("source = %q, want plugin:riskctl", events[0].Source)
	}
}
