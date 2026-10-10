package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	// The docs say the budget is exhausted after more than 30 wrong guesses, so
	// the 31st is the last one let through.
	if passed != addressFreeAttempts+1 {
		t.Errorf("%d wrong codes got through the budget, want %d", passed, addressFreeAttempts+1)
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

// An IPv6 address is counted by its whole /64, the unit one subscriber is handed,
// so rotating through one /64 gets no fresh allowance. A neighbouring /64 is
// somebody else and is not affected.
func TestTheInviteBudgetKeysAnIPv6AddressByItsWhole64(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	wireInvites(f)

	var limited *RateLimitError
	refused := false
	for guess := 0; guess < 3*addressFreeAttempts && !refused; guess++ {
		_, _, err := f.auth.Register(ctx, RegisterInput{
			Username: "rotator" + strconv.Itoa(guess), Password: "a-good-password",
			IP: "2001:db8:0:1::" + strconv.FormatInt(int64(guess)+1, 16), InviteCode: "WRONG" + strconv.Itoa(guess),
		})
		refused = errors.As(err, &limited)
	}
	if !refused {
		t.Fatal("rotating through one /64 was never refused")
	}

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Password: "a-good-password", IP: "2001:db8:0:1:ffff::9", InviteCode: "RIGHT2",
	}); !errors.As(err, &limited) {
		t.Fatalf("the right code from the same /64 = %v, want the budget refusal", err)
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "neighbour", Password: "a-good-password", IP: "2001:db8:0:2::1", InviteCode: "RIGHT2",
	}); err != nil {
		t.Fatalf("the right code from a neighbouring /64 = %v, want it accepted", err)
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
			// The docs promise the header as well as the body field, and a client
			// that backs off on Retry-After must not be left guessing.
			if got := response.Header().Get("Retry-After"); got != "1" && got != "2" {
				t.Fatalf("Retry-After = %q, want 1 or 2 seconds", got)
			}
			return
		}
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invite_invalid"`) {
			t.Fatalf("guess %d = %d %s, want invite_invalid", n, response.Code, response.Body.String())
		}
	}
	t.Fatal("wrong invite codes from one address were never answered with 429")
}

// The budget counts requests, so a burst that arrives together must not all get
// past it before any of them has been counted. Each request that does get past
// runs the paid review and then checks its code; the rest are refused before
// either. Real goroutines, because the failure is a race: sequential requests
// are counted one at a time and never show it.
func TestABurstOfWrongCodesFromOneAddressIsCountedOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	wireInvites(f)
	var reviews atomic.Int64
	f.auth.ReviewSignup = func(context.Context, RegisterInput, int) (SignupReview, error) {
		reviews.Add(1)
		// Long enough that every request in the burst is in flight at once.
		time.Sleep(20 * time.Millisecond)
		return SignupReview{Decision: SignupAllow}, nil
	}

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
			_, _, err := f.auth.Register(ctx, RegisterInput{
				Username: "burst" + strconv.Itoa(i), Password: "a-good-password",
				IP: "198.51.100.71", InviteCode: "WRONG" + strconv.Itoa(i),
			})
			var rl *RateLimitError
			switch {
			case errors.As(err, &rl):
				limited.Add(1)
			case errors.Is(err, ErrInviteInvalid):
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
	// Exactly the budget gets through: nothing that reaches the code check is
	// ever handed back, so the places taken in the burst are never freed early.
	if got := reviews.Load(); got != addressFreeAttempts+1 {
		t.Errorf("paid review ran %d times for a burst of %d wrong codes, want %d", got, burst, addressFreeAttempts+1)
	}
	if checked.Load() != reviews.Load() {
		t.Errorf("%d code checks for %d paid reviews, want one check for each review", checked.Load(), reviews.Load())
	}
	if checked.Load()+limited.Load() != burst {
		t.Errorf("checked %d + limited %d != burst %d", checked.Load(), limited.Load(), burst)
	}
}
