package card

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
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

// The address half of the limit is what stops one host working through many
// accounts. An IPv6 subscriber holds a whole /64, so keying on the full
// address gave each guess a fresh bucket.
func TestGuessesFromOneIPv6SubnetShareAnAllowance(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.store)

	calls := 0
	handlers.ClientIP = func(*http.Request) string {
		calls++
		return fmt.Sprintf("2001:db8:7:7::%x", calls)
	}
	mux := http.NewServeMux()
	handlers.Routes(mux)

	var throttled bool
	for i := range 40 {
		// A different account each time, so only the address can add up.
		reader := f.reader(t, fmt.Sprintf("guesser-%d", i))
		request := httptest.NewRequest(http.MethodPost, "/api/usage/redeem",
			strings.NewReader(`{"code":"NOPE"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(auth.WithUser(context.Background(), reader))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("forty wrong codes from one /64 were all answered; each address got its own allowance")
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

// The browser dialog is presentation; this is the boundary that makes it a
// requirement. A caller that skips the dialog must not consume the code, and
// the token that passes is bound to the same address used by the guess limit.
func TestRedemptionCanRequireTurnstile(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.store)
	reader := f.reader(t, "challenged-reader")
	ctx := context.Background()

	if _, err := f.store.CreateCodes(ctx, CodeInput{Code: "HUMAN", Cards: 1}, 1); err != nil {
		t.Fatal(err)
	}

	const address = "203.0.113.12"
	verified := 0
	handlers.ClientIP = func(*http.Request) string { return address }
	handlers.Challenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify: func(_ context.Context, token, ip string) error {
			verified++
			if ip != address {
				t.Errorf("challenge IP = %q, want %q", ip, address)
			}
			if token != "solved" {
				return turnstile.ErrFailed
			}
			return nil
		},
	}

	mux := http.NewServeMux()
	handlers.Routes(mux)
	ask := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/usage/redeem",
			strings.NewReader(`{"code":"HUMAN","turnstile":"`+token+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(auth.WithUser(ctx, reader))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		return recorder
	}

	withoutProof := ask("")
	if withoutProof.Code != http.StatusForbidden ||
		!strings.Contains(withoutProof.Body.String(), `"code":"challenge_failed"`) {
		t.Fatalf("redemption without proof = %d %s", withoutProof.Code, withoutProof.Body.String())
	}
	if withProof := ask("solved"); withProof.Code != http.StatusCreated {
		t.Fatalf("redemption with proof = %d %s", withProof.Code, withProof.Body.String())
	}
	if verified != 2 {
		t.Errorf("challenge checks = %d, want 2", verified)
	}
}

// A browser that goes away as it spends a card must not leave the card spent
// with no reset. The request arrives already cancelled here, which is what a
// disconnect looks like to the handler, and the mark and the reset must still
// run and see a live context.
func TestASpentCardIsResetEvenWhenTheBrowserHasGone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "spender")
	cards, err := f.store.Grant(ctx, nil, reader.ID, 1, 30)
	if err != nil || len(cards) != 1 {
		t.Fatalf("grant: %v %v", cards, err)
	}

	handlers := NewHandlers(f.store)
	handlers.LockReset = func(context.Context, database.Queryer) error { return nil }
	// Read while the handler is still running: the context is released when
	// the handler returns, and that release is not what this test is about.
	var (
		reset    bool
		resetErr error
	)
	handlers.OnSpend = func(ctx context.Context, _ database.Queryer, _ user.User, _ Card) error {
		reset, resetErr = true, ctx.Err()
		return nil
	}
	mux := http.NewServeMux()
	handlers.Routes(mux)

	gone, cancel := context.WithCancel(ctx)
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/usage/cards/"+cards[0].ID+"/use", nil)
	request = request.WithContext(auth.WithUser(gone, reader))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("spend answered %d, want the card spent", recorder.Code)
	}
	if !reset {
		t.Fatal("the card was spent but its reset never ran")
	}
	if resetErr != nil {
		t.Fatalf("the reset ran on a context the disconnect cancelled: %v", resetErr)
	}
}

// The mark and the reset are one transaction. A reset that fails must leave the
// card unspent, so the account can press again once the fault is gone; a card
// marked spent with no reset behind it is the loss this exists to prevent.
func TestASpentCardStaysUnspentWhenItsResetFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "failed-reset")
	cards, err := f.store.Grant(ctx, nil, reader.ID, 1, 30)
	if err != nil || len(cards) != 1 {
		t.Fatalf("grant: %v %v", cards, err)
	}

	handlers := NewHandlers(f.store)
	handlers.LockReset = func(context.Context, database.Queryer) error { return nil }
	handlers.OnSpend = func(context.Context, database.Queryer, user.User, Card) error {
		return errors.New("counters unavailable")
	}
	mux := http.NewServeMux()
	handlers.Routes(mux)

	request := httptest.NewRequest(http.MethodPost, "/api/usage/cards/"+cards[0].ID+"/use", nil)
	request = request.WithContext(auth.WithUser(ctx, reader))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("a failed reset answered %d, want 500", recorder.Code)
	}
	if left, err := f.store.Available(ctx, reader.ID); err != nil || len(left) != 1 {
		t.Fatalf("the card was spent though its reset failed: %v (%v)", left, err)
	}
}
