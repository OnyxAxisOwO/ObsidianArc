package auth

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The account exists once Register has committed, so a browser that goes away
// while the sign-up is finishing must not cancel the reward for it. The verify
// path already detaches the context it hands RewardInvite; the register path
// used to pass the request's own, and so lost the reward to a disconnect.
func TestRegistrationRewardSurvivesADisconnect(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}

	reqCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	f.auth.Hooks.ReviewSignup = func(context.Context, RegisterInput, int) (SignupReview, error) {
		return SignupReview{Ran: true, Decision: SignupAllow}, nil
	}
	// The review's record is the last thing the request does before the reward,
	// so the browser going away at that point is the case that matters.
	f.auth.Hooks.OnSignupReview = func(context.Context, RegisterInput, *user.User, SignupReview) {
		disconnect()
	}
	var offered bool
	var rewardErr error
	f.auth.Hooks.RewardInvite = func(rewardCtx context.Context, _ string, _ bool) {
		offered = true
		rewardErr = rewardCtx.Err()
	}

	if _, _, err := f.auth.Register(reqCtx, RegisterInput{Username: "member", Password: "another-password"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !offered {
		t.Fatal("the reward was not offered after registration")
	}
	if rewardErr != nil {
		t.Fatalf("the reward ran on a cancelled context: %v", rewardErr)
	}
}
