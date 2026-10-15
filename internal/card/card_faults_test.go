package card

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
)

// spendRace lets another spend land on the card this spend has just chosen,
// between its read and its conditional UPDATE. A manual use does not take the
// account's row lock, so that window is real; opening it here keeps the test
// from depending on scheduling.
type spendRace struct {
	database.Queryer
	t     *testing.T
	taken string
}

func (r *spendRace) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r.taken == "" && strings.HasPrefix(query, "UPDATE usage_cards SET used_at = ?") {
		r.taken = args[1].(string)
		if _, err := r.Queryer.Exec(ctx,
			`UPDATE usage_cards SET used_at = ? WHERE id = ? AND used_at = ?`,
			time.Now().UnixMilli(), r.taken, 0); err != nil {
			r.t.Fatal(err)
		}
	}
	return r.Queryer.Exec(ctx, query, args...)
}

// A card the automatic reset picked can be spent by someone else before its
// UPDATE runs. The reset has another card and must use it: reporting "none
// left" sends the account a 429 while it still holds one.
func TestAutomaticResetPicksAnotherCardWhenItsChoiceWasSpentFirst(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "racer")
	if _, err := f.store.Grant(ctx, nil, reader.ID, 2, 30); err != nil {
		t.Fatal(err)
	}

	race := &spendRace{Queryer: f.db, t: t}
	spent, err := f.store.spendNextLocked(ctx, race, reader.ID, "")
	if err != nil {
		t.Fatalf("reset refused with a card still unused: %v", err)
	}
	if race.taken == "" || spent.ID == race.taken {
		t.Fatalf("reset spent %q after the race took %q, want the other card", spent.ID, race.taken)
	}
	if left, err := f.store.Available(ctx, reader.ID); err != nil || len(left) != 0 {
		t.Fatalf("after two resets left %v (%v), want none", left, err)
	}
}

// A code past its expiry is refused by every redemption with the same answer
// as a typo, so an operator who mints one is never told why. Creation refuses
// what GrantUntilNamed refuses: a past date, a seconds timestamp where
// milliseconds were meant, and anything beyond MaxDays.
func TestACodeCannotBeMintedAlreadyExpired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	refused := map[string]int64{
		"past":    now - 1000,
		"seconds": now/1000 + 3600,
		"beyond":  now + int64(MaxDays+1)*24*3600*1000,
	}
	for name, expires := range refused {
		if _, err := f.store.CreateCode(ctx, CodeInput{Code: "EXPIRY-" + strings.ToUpper(name), Cards: 1, ExpiresAt: expires}); !errors.Is(err, ErrInvalidExpiry) {
			t.Errorf("a code expiring %s was created: %v, want ErrInvalidExpiry", name, err)
		}
	}

	future := now + 24*3600*1000
	made, err := f.store.CreateCode(ctx, CodeInput{Code: "FUTURE", Cards: 1, ExpiresAt: future})
	if err != nil || made.ExpiresAt != future {
		t.Fatalf("a valid future expiry was refused or changed: %+v %v", made, err)
	}
	if _, err := f.store.CreateCode(ctx, CodeInput{Code: "FOREVER", Cards: 1}); err != nil {
		t.Fatalf("a code without an expiry was refused: %v", err)
	}
}

// A foreign-key failure on the redemption insert is not a second claim on the
// same code. Reporting it as "already redeemed" tells the account something
// untrue about a row that no longer exists.
func TestAForeignKeyFailureIsNotReportedAsAnAlreadyRedeemedCode(t *testing.T) {
	for message, want := range map[string]bool{
		"UNIQUE constraint failed: redemptions.code_id":           true,
		`duplicate key value violates unique constraint "x_pkey"`: true,
		"FOREIGN KEY constraint failed":                           false,
		"CHECK constraint failed: cards >= 0":                     false,
		"NOT NULL constraint failed: redemptions.user_id":         false,
	} {
		if got := isUnique(errors.New(message)); got != want {
			t.Errorf("isUnique(%q) = %v, want %v", message, got, want)
		}
	}

	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.store.CreateCodes(ctx, CodeInput{Code: "GONE-ACCOUNT", Cards: 1}, 1); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.Redeem(ctx, "no-such-account", "GONE-ACCOUNT")
	if err == nil || errors.Is(err, ErrCodeUsed) {
		t.Fatalf("redeeming as an account that does not exist answered %v, want a failure other than 'already redeemed'", err)
	}
}

// A handler built without its reset hook must not spend a card. The card is
// the thing a person paid for, and a 204 for it with nothing restored is the
// silent version of the failure.
func TestUseRefusesToSpendACardWhenNoResetIsWired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "unwired")
	cards, err := f.store.Grant(ctx, nil, reader.ID, 1, 30)
	if err != nil || len(cards) != 1 {
		t.Fatalf("grant: %v %v", cards, err)
	}

	handlers := NewHandlers(f.store)
	mux := http.NewServeMux()
	handlers.Routes(mux)
	request := httptest.NewRequest(http.MethodPost, "/api/usage/cards/"+cards[0].ID+"/use", nil)
	request = request.WithContext(auth.WithUser(ctx, reader))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("spend without a reset answered %d, want 500", recorder.Code)
	}
	if left, err := f.store.Available(ctx, reader.ID); err != nil || len(left) != 1 {
		t.Fatalf("the card was spent with no reset wired: %v (%v)", left, err)
	}
}

// A refused challenge is a failed guess, and a request the limiter has stopped
// must not cost a round trip to the challenge service. Without counting the
// refusal, a caller who never solves one could make verification calls without
// limit.
func TestRefusedChallengesStopAtTheLimiterWithoutAnotherVerification(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.store)
	reader := f.reader(t, "challenged")
	ctx := context.Background()

	verified := 0
	handlers.ClientIP = func(*http.Request) string { return "198.51.100.7" }
	handlers.Challenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify: func(context.Context, string, string) error {
			verified++
			return turnstile.ErrFailed
		},
	}
	mux := http.NewServeMux()
	handlers.Routes(mux)
	ask := func() int {
		request := httptest.NewRequest(http.MethodPost, "/api/usage/redeem",
			strings.NewReader(`{"code":"NOPE","turnstile":"forged"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(auth.WithUser(ctx, reader))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		return recorder.Code
	}

	throttled := false
	for range 30 {
		if ask() == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("thirty refused challenges were all answered; nothing counted them as failed attempts")
	}
	atRefusal := verified
	for range 5 {
		ask()
	}
	if verified != atRefusal {
		t.Fatalf("the challenge service was called %d more times after the limiter refused", verified-atRefusal)
	}
}
