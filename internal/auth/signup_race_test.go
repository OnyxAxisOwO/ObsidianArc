package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Two processes over one database take sign-ups at the same moment. Between them
// they may spend the instance's per-minute allowance once, and no more: each
// process has its own in-memory gate, so only the count in the database can stop
// the second process from granting what the first has already spent.
func TestSignupsRacingAcrossTwoProcessesStopAtTheLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")
	set(t, f, settings.SignupsPerMinute, "4")

	cfg := config.Config{
		Password:  testParams(),
		Session:   config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
		SecretKey: []byte("an-instance-secret-for-the-auth-tests"),
	}
	second := NewService(f.db, f.users, f.groups, f.settings, mail.New(mail.Config{}), cfg)
	processes := []*Service{f.auth, second}

	// The founder is the first sign-up in the minute, so three more fit.
	const attempts = 12
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
		refused  int
		other    []error
	)
	gate := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			_, _, err := processes[i%len(processes)].Register(ctx, RegisterInput{
				Username: fmt.Sprintf("racer%d", i), Password: "a-good-password",
			})
			var throttled *SignupThrottleError
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.As(err, &throttled):
				refused++
			default:
				other = append(other, err)
			}
		}(i)
	}
	close(gate)
	wg.Wait()

	if len(other) != 0 {
		t.Fatalf("unexpected results: %v", other)
	}
	if accepted != 3 || refused != attempts-3 {
		t.Fatalf("%d sign-ups accepted and %d refused across two processes, want 3 and %d", accepted, refused, attempts-3)
	}
}

// A sign-up that read an empty instance skipped the challenges, and a first
// account committed before its transaction. Where a challenge applies, that
// sign-up must not open an account without answering it: it is sent back to be
// made again, and the retry runs the challenges.
func TestASignUpThatRacedTheFirstAccountIsSentBackNotOpened(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// The review being live is what makes the racing sign-up a challenge that
	// applies. Without it the sign-up opens, as the next test pins.
	set(t, f, settings.SignupReview, "true")
	set(t, f, settings.SignupReviewModel, "review-model")
	f.auth.ReviewSignup = func(context.Context, RegisterInput, int) (SignupReview, error) {
		return SignupReview{Ran: true, Decision: SignupAllow}, nil
	}

	raced := false
	f.auth.hooks.afterPreflight = func() {
		if raced {
			return
		}
		raced = true
		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "founder", Password: "a-good-password",
		}); err != nil {
			t.Errorf("the first account: %v", err)
		}
	}

	_, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "newcomer", Password: "another-password",
	})
	if !errors.Is(err, errSignupRetry) {
		t.Fatalf("the racing sign-up: err = %v, want it sent back to be made again", err)
	}
	if total, err := f.users.Count(ctx, nil); err != nil || total != 1 {
		t.Fatalf("accounts = %d (err %v), want only the founder", total, err)
	}

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "newcomer", Password: "another-password",
	}); err != nil {
		t.Fatalf("the sign-up sent again: %v", err)
	}
}

// A review that is wired but switched off, with no captcha, PoW or guard
// configured, challenges nothing. The racing sign-up opens as it would have
// without the race check, rather than being sent back on every such instance.
func TestASignUpThatRacedTheFirstAccountOpensWhenNoChallengeIsConfigured(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.auth.ReviewSignup = func(context.Context, RegisterInput, int) (SignupReview, error) {
		return SignupReview{Ran: true, Decision: SignupAllow}, nil
	}

	raced := false
	f.auth.hooks.afterPreflight = func() {
		if raced {
			return
		}
		raced = true
		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "founder", Password: "a-good-password",
		}); err != nil {
			t.Errorf("the first account: %v", err)
		}
	}

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "newcomer", Password: "another-password",
	}); err != nil {
		t.Fatalf("the racing sign-up on an unchallenged instance: %v", err)
	}
	if total, err := f.users.Count(ctx, nil); err != nil || total != 2 {
		t.Fatalf("accounts = %d (err %v), want the founder and the newcomer", total, err)
	}
}
