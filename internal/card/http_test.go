package card

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
)

// A redemption code is a secret somebody types, and an administrator may
// deliberately mint a memorable one. Sign-in slows a guesser down; this
// endpoint did not, so a dictionary could be run against it at full speed
// from any signed-in account.
func TestGuessingAtRedemptionCodesIsThrottled(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.store)
	reader := f.reader(t, "guesser")

	mux := http.NewServeMux()
	handlers.Routes(mux)

	ask := func(code string) int {
		request := httptest.NewRequest(http.MethodPost, "/api/usage/redeem",
			strings.NewReader(`{"code":"`+code+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(auth.WithUser(context.Background(), reader))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		return recorder.Code
	}

	// The allowance is generous enough that somebody mistyping a code never
	// meets it; what follows is a run of guesses, which does.
	var throttled bool
	for i := range 20 {
		if status := ask("NOPE" + string(rune('A'+i))); status == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("twenty wrong codes in a row were all answered; nothing slows a dictionary down")
	}
}

// A code that exists and simply is not for this account is a typo, not a
// search, and must not spend the allowance that stops one.
func TestRedeemingTheSameCodeTwiceIsNotTreatedAsGuessing(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.store)
	reader := f.reader(t, "reader")
	ctx := context.Background()

	if _, err := f.store.CreateCodes(ctx, CodeInput{Code: "WELCOME", Cards: 50}, 1); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	handlers.Routes(mux)

	ask := func() int {
		request := httptest.NewRequest(http.MethodPost, "/api/usage/redeem",
			strings.NewReader(`{"code":"WELCOME"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(auth.WithUser(ctx, reader))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if status := ask(); status != http.StatusCreated {
		t.Fatalf("first redemption gave %d, want 201", status)
	}
	for range 15 {
		if status := ask(); status == http.StatusTooManyRequests {
			t.Fatal("repeating a real code was counted as guessing")
		}
	}
}
