package oauth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

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
