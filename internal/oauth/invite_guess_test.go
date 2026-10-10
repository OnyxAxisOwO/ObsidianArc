package oauth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// guessesAllowed is how many wrong codes one address gets through the budget
// before it is refused: 30 free attempts and one more, as sign-in's budget gives.
// The count is written out here because it is the documented behaviour, and the
// auth package keeps the number unexported.
const guessesAllowed = 31

// The completion form is a second place an invite code is guessed. The provider
// has already said who is at the browser, so each attempt at the form is one
// more guess at the code, and it is held to the budget a registration is.
func TestAGuessThroughTheCompletionFormRunsOut(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.auth.ConsumeInvite = func(context.Context, *database.Tx, string) (*auth.InviteGrant, error) {
		return nil, errors.New("invite: that code is not valid")
	}
	ctx := context.Background()

	sawLimit := false
	for guess := 0; guess < 90 && !sawLimit; guess++ {
		n := strconv.Itoa(guess)
		_, err := f.service.Complete(ctx, identity("guess-"+n, "guest"+n, ""), Details{
			Username: "guest" + n, Invite: "WRONG" + n,
		}, cleared, "203.0.113.61", "a browser")
		var limited *auth.RateLimitError
		switch {
		case errors.As(err, &limited):
			sawLimit = true
		case errors.Is(err, auth.ErrInviteInvalid):
		default:
			t.Fatalf("guess %d through the form = %v, want the code refused", guess, err)
		}
	}
	if !sawLimit {
		t.Fatal("wrong codes through the completion form were never refused")
	}
}

// The budget reaches the form as a 429 the form can word, with the code it
// shares with the sign-up form.
func TestTheCompletionFormAnswersTheBudgetAsTooManyAttempts(t *testing.T) {
	mapped := completionError(&auth.RateLimitError{RetryAfter: 3 * time.Second})
	var response *httpx.Error
	if !errors.As(mapped, &response) {
		t.Fatalf("completion mapping = %T %v, want httpx.Error", mapped, mapped)
	}
	if response.Status != http.StatusTooManyRequests || response.Code != "too_many_attempts" {
		t.Errorf("completion mapping = %d/%s, want 429/too_many_attempts", response.Status, response.Code)
	}
}

// A burst through the form is counted the way a burst through registration is:
// each request that gets past the budget runs the paid screening and then checks
// its code, and the rest are refused before either. Real goroutines, because the
// failure is a race that sequential guesses never show.
func TestABurstOfWrongCodesThroughTheFormIsCountedOnce(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, settings.OAuthAllowSignup, "true")
	f.auth.ConsumeInvite = func(context.Context, *database.Tx, string) (*auth.InviteGrant, error) {
		return nil, errors.New("invite: that code is not valid")
	}
	var screened atomic.Int64
	f.auth.ScreenEmail = func(context.Context, string) error {
		screened.Add(1)
		// Long enough that every request in the burst is in flight at once.
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	ctx := context.Background()

	const burst = 200
	var (
		wg                      sync.WaitGroup
		start                   = make(chan struct{})
		checked, limited, other atomic.Int64
	)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			n := strconv.Itoa(i)
			_, err := f.service.Complete(ctx, identity("burst-"+n, "burst"+n, "burst"+n+"@example.com"),
				Details{Username: "burst" + n, Invite: "WRONG" + n}, cleared, "203.0.113.81", "a browser")
			var rl *auth.RateLimitError
			switch {
			case errors.As(err, &rl):
				limited.Add(1)
			case errors.Is(err, auth.ErrInviteInvalid):
				checked.Add(1)
			default:
				other.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got := other.Load(); got != 0 {
		t.Fatalf("%d of the burst failed for some other reason", got)
	}
	if got := screened.Load(); got != guessesAllowed {
		t.Errorf("paid screening ran %d times for a burst of %d wrong codes, want %d", got, burst, guessesAllowed)
	}
	if checked.Load() != screened.Load() {
		t.Errorf("%d code checks for %d paid screenings, want one check for each", checked.Load(), screened.Load())
	}
	if checked.Load()+limited.Load() != burst {
		t.Errorf("checked %d + limited %d != burst %d", checked.Load(), limited.Load(), burst)
	}
}

// The form's refusal is the same 429 the sign-up form sends, and it carries the
// Retry-After header the docs promise, not only the field in the body.
func TestTheCompletionFormSendsRetryAfterWithTheBudget(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	// Asking for a username is what parks the sign-up at the form; without it the
	// callback opens the account and there is no form to guess at.
	require(t, f, settings.OAuthRequireUsername, "true")
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	f.auth.ConsumeInvite = func(context.Context, *database.Tx, string) (*auth.InviteGrant, error) {
		return nil, errors.New("invite: that code is not valid")
	}
	_, mux := handlers(t, f)
	ticket := pendingTicket(t, mux)

	for n := 0; n < 3*guessesAllowed; n++ {
		number := strconv.Itoa(n)
		done := postJSON(mux, "/api/auth/oauth/signup",
			map[string]any{"username": "guest" + number, "invite_code": "WRONG" + number}, []*http.Cookie{ticket})
		if done.Code == http.StatusBadRequest && strings.Contains(done.Body.String(), `"invite_invalid"`) {
			continue
		}
		if done.Code != http.StatusTooManyRequests || !strings.Contains(done.Body.String(), `"too_many_attempts"`) {
			t.Fatalf("guess %d = %d %s, want invite_invalid or too_many_attempts", n, done.Code, done.Body.String())
		}
		if got := done.Header().Get("Retry-After"); got != "1" && got != "2" {
			t.Fatalf("Retry-After = %q, want 1 or 2 seconds", got)
		}
		return
	}
	t.Fatal("wrong codes through the completion form were never answered with 429")
}
