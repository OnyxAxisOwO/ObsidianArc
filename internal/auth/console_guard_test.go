package auth

import (
	"context"
	"errors"
	"testing"
)

// A login guard that refuses the sign-in form has to refuse the console's door
// as well, or the operator's refusal is one password away from being ignored.
func TestVerifyCredentialIsRefusedByTheLoginGuardThatRefusesTheSignInForm(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	f.auth.AddGuard(GuardLogin, Guard{
		Name:   "risk",
		Plugin: "risk",
		Event:  "login_refused",
		Check: func(context.Context, GuardRequest) (Verdict, error) {
			return Verdict{}, &GuardRefusal{Err: errors.New("refused by the risk service"), Reason: "score"}
		},
	})

	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password", IP: "198.51.100.7"}); err == nil {
		t.Fatal("control: the sign-in form should be refused by the guard")
	}

	_, _, err := f.auth.VerifyCredential(ctx, "arc", "a-good-password", "198.51.100.7")
	var refusal *GuardRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("console verify with the right password = %v, want the guard's refusal", err)
	}
}

// The console asks its guards the question the sign-in form asks, with no token
// of its own: a connection over SSH has no browser to have solved one.
func TestTheConsoleAsksTheLoginGuardsWhatTheSignInFormAsks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	var asked []GuardRequest
	f.auth.AddGuard(GuardLogin, Guard{
		Name:   "risk",
		Plugin: "risk",
		Event:  "login_refused",
		Check: func(_ context.Context, request GuardRequest) (Verdict, error) {
			asked = append(asked, request)
			return Verdict{}, nil
		},
	})

	if _, _, err := f.auth.VerifyCredential(ctx, "ARC", "a-good-password", "198.51.100.7"); err != nil {
		t.Fatalf("verify with a guard that allows it: %v", err)
	}
	if len(asked) != 1 {
		t.Fatalf("guard asked %d times, want once", len(asked))
	}
	got := asked[0]
	if got.Action != GuardLogin || got.Token != "" || got.IP != "198.51.100.7" || got.Username != "ARC" {
		t.Errorf("guard asked %+v, want the login door with no token, the address, and the identifier as typed", got)
	}
}
