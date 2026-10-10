package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// wireInvites stands a one-code invite hook in front of the service the way
// server.go wires invite.Store: the code "RIGHT2" spends a use, any other code
// is refused.
func wireInvites(f *fixture) {
	f.auth.ConsumeInvite = func(_ context.Context, _ *database.Tx, code string) (*InviteGrant, error) {
		if code != "RIGHT2" {
			return nil, errors.New("invite: that code is not valid")
		}
		return &InviteGrant{CodeID: "code-1"}, nil
	}
}

// A short invite code is a small space to search, and a registration is the
// cheapest place to search it. Guesses from one address spend that address's
// allowance, and a guess the allowance refuses costs nothing: it does not reach
// the paid review, the screening or the hash.
func TestWrongInviteCodesFromOneAddressRunOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	wireInvites(f)
	reviews := 0
	f.auth.ReviewSignup = func(context.Context, RegisterInput, int) (SignupReview, error) {
		reviews++
		return SignupReview{Decision: SignupAllow}, nil
	}

	const address = "198.51.100.21"
	var limited *RateLimitError
	passed, refused := 0, 0
	for guess := 0; guess < 3*addressFreeAttempts; guess++ {
		_, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "guesser" + strconv.Itoa(guess), Password: "a-good-password",
			IP: address, InviteCode: "WRONG" + strconv.Itoa(guess),
		})
		switch {
		case errors.As(err, &limited):
			refused++
		case errors.Is(err, ErrInviteInvalid):
			passed++
		default:
			t.Fatalf("guess %d = %v, want the code refused or the budget", guess, err)
		}
	}
	if refused == 0 {
		t.Fatalf("%d wrong codes from one address were never refused", passed)
	}
	if reviews != passed {
		t.Errorf("paid review ran %d times for %d guesses that got through the budget", reviews, passed)
	}
}

// The budget belongs to the address that guessed. The same address is refused
// with the right code too, because a correct guess from a blocked address is a
// way round the limit; another address is not affected at all.
func TestOnlyTheAddressThatGuessedIsSlowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	wireInvites(f)

	const guesser, other = "198.51.100.31", "198.51.100.32"
	var limited *RateLimitError
	sawLimit := false
	for guess := 0; guess < 3*addressFreeAttempts && !sawLimit; guess++ {
		_, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "guesser" + strconv.Itoa(guess), Password: "a-good-password",
			IP: guesser, InviteCode: "WRONG" + strconv.Itoa(guess),
		})
		sawLimit = errors.As(err, &limited)
	}
	if !sawLimit {
		t.Fatal("the guesser was never refused")
	}

	_, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Password: "a-good-password", IP: guesser, InviteCode: "RIGHT2",
	})
	if !errors.As(err, &limited) {
		t.Fatalf("the right code from the guessing address = %v, want the budget refusal", err)
	}

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Password: "a-good-password", IP: other, InviteCode: "RIGHT2",
	}); err != nil {
		t.Fatalf("the right code from another address = %v, want it accepted", err)
	}
}

// A code that works is not a guess, so an address that uses valid codes is never
// slowed by them.
func TestAValidInviteCodeIsNotCountedAsAGuess(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	wireInvites(f)

	for n := 0; n < 2*addressFreeAttempts; n++ {
		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "member" + strconv.Itoa(n), Password: "a-good-password",
			IP: "198.51.100.41", InviteCode: "RIGHT2",
		}); err != nil {
			t.Fatalf("valid registration %d = %v", n, err)
		}
	}
}

// Over HTTP the budget is an answer the sign-up form can word: 429 with the code
// too_many_attempts, not a server error.
func TestAnExhaustedInviteBudgetAnswersTooManyAttempts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	wireInvites(f)

	handler := &Handlers{service: f.auth, groups: f.groups}
	register := httpx.Wrap(handler.register)
	post := func(n int, code string) *httptest.ResponseRecorder {
		body := `{"username":"guest` + strconv.Itoa(n) + `","password":"a-good-password","invite_code":"` + code + `"}`
		request := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "198.51.100.51:44321"
		recorder := httptest.NewRecorder()
		register.ServeHTTP(recorder, request)
		return recorder
	}

	for n := 0; n < 3*addressFreeAttempts; n++ {
		response := post(n, "WRONG"+strconv.Itoa(n))
		if response.Code == http.StatusTooManyRequests {
			if !strings.Contains(response.Body.String(), `"too_many_attempts"`) {
				t.Fatalf("refused with %d %s, want the too_many_attempts code", response.Code, response.Body.String())
			}
			return
		}
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invite_invalid"`) {
			t.Fatalf("guess %d = %d %s, want invite_invalid", n, response.Code, response.Body.String())
		}
	}
	t.Fatal("wrong invite codes from one address were never answered with 429")
}
