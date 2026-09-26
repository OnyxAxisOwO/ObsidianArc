package card

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	store *Store
	users *user.Store
	db    *database.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "card.db"),
		MaxOpenConns: 8,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	return &fixture{store: NewStore(db), users: user.NewStore(db), db: db}
}

func (f *fixture) reader(t *testing.T, username string) user.User {
	t.Helper()
	account, err := f.users.Create(context.Background(), nil, user.CreateInput{
		Username: username, PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive,
	})
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	return account
}

// A code carries a fixed number of cards, and a crowd arriving at once cannot
// take more than that between them.
//
// The claim is a conditional UPDATE rather than a read followed by a write,
// which is the whole point of this test: with the check outside the
// statement, every goroutine reads the same `claimed` and every one of them
// wins.
func TestACodeCannotBeOverRedeemed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	const seats = 5
	const crowd = 20
	if _, err := f.store.CreateCode(ctx, CodeInput{Code: "LAUNCH", Cards: seats, CardDays: 30}); err != nil {
		t.Fatal(err)
	}

	people := make([]user.User, 0, crowd)
	for i := range crowd {
		people = append(people, f.reader(t, fmt.Sprintf("person-%d", i)))
	}

	var (
		wait      sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		empty     int
	)
	start := make(chan struct{})
	for _, person := range people {
		wait.Add(1)
		go func(account user.User) {
			defer wait.Done()
			<-start
			_, err := f.store.Redeem(ctx, account.ID, "LAUNCH")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrCodeEmpty):
				empty++
			}
		}(person)
	}
	close(start)
	wait.Wait()

	if succeeded != seats {
		t.Errorf("%d of %d redeemed a %d-card code", succeeded, crowd, seats)
	}
	if empty != crowd-seats {
		t.Errorf("%d were told the code was empty, want %d", empty, crowd-seats)
	}

	// And the counter agrees with what was handed out, rather than with how
	// many people asked.
	var claimed int
	if err := f.db.QueryRow(ctx,
		`SELECT claimed FROM redemption_codes WHERE code = ?`, "LAUNCH").Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if claimed != seats {
		t.Errorf("claimed = %d, want %d", claimed, seats)
	}
}

// One card is one reset. Two tabs pressing the button together must not spend
// it twice, or a card is worth as many resets as somebody can double-click.
func TestACardIsSpentOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "spender")

	granted, err := f.store.Grant(ctx, nil, person.ID, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	cardID := granted[0].ID

	var (
		wait   sync.WaitGroup
		mu     sync.Mutex
		spent  int
		reused int
	)
	start := make(chan struct{})
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			err := f.store.Spend(ctx, person.ID, cardID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				spent++
			case errors.Is(err, ErrUsed):
				reused++
			}
		}()
	}
	close(start)
	wait.Wait()

	if spent != 1 {
		t.Errorf("the card was spent %d times, want once", spent)
	}
	if reused != 7 {
		t.Errorf("%d callers were told it was used, want 7", reused)
	}
}

// The same account cannot take two cards from one code, however many times it
// tries. That rule is the primary key on redemptions, not a count.
func TestOneAccountRedeemsACodeOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "keen")

	if _, err := f.store.CreateCode(ctx, CodeInput{Code: "TWICE", Cards: 10, CardDays: 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem(ctx, person.ID, "TWICE"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem(ctx, person.ID, "TWICE"); !errors.Is(err, ErrCodeUsed) {
		t.Errorf("a second redemption gave %v, want ErrCodeUsed", err)
	}

	cards, err := f.store.Available(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Errorf("holds %d cards, want 1", len(cards))
	}
}

// Redemption details are administrative identity data, kept out of the code
// list's hot path and loaded only when an operator opens one code.
func TestCodeRedemptionsNameTheAccountsThatClaimedIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.reader(t, "alice")
	bob := f.reader(t, "bob")

	code, err := f.store.CreateCode(ctx, CodeInput{Code: "WHO", Cards: 10, CardDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem(ctx, alice.ID, code.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem(ctx, bob.ID, code.Code); err != nil {
		t.Fatal(err)
	}

	records, err := f.store.CodeRedemptions(ctx, code.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("redemptions = %d, want 2", len(records))
	}
	seen := map[string]string{}
	for _, record := range records {
		seen[record.UserID] = record.Username
		if record.RedeemedAt <= 0 {
			t.Errorf("redemption for %q has no time", record.Username)
		}
	}
	if seen[alice.ID] != "alice" || seen[bob.ID] != "bob" {
		t.Errorf("redemption identities = %+v", seen)
	}
}

// An expired card is not offered and cannot be spent. Both halves matter: the
// listing is what a reader sees, and the spend is what actually decides.
func TestAnExpiredCardIsNeitherOfferedNorSpent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "late")

	granted, err := f.store.Grant(ctx, nil, person.ID, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE usage_cards SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), granted[0].ID); err != nil {
		t.Fatal(err)
	}

	cards, err := f.store.Available(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Errorf("an expired card was still offered: %+v", cards)
	}
	if err := f.store.Spend(ctx, person.ID, granted[0].ID); !errors.Is(err, ErrExpired) {
		t.Errorf("spending an expired card gave %v, want ErrExpired", err)
	}
}

// A hand-issued card carries the date the operator chose, not a fresh
// thirty-day calculation made after the request reached the server.
func TestGrantUntilKeepsTheChosenExpiry(t *testing.T) {
	f := newFixture(t)
	person := f.reader(t, "dated")
	expires := time.Now().Add(45 * 24 * time.Hour).Truncate(time.Second).UnixMilli()

	granted, err := f.store.GrantUntil(context.Background(), person.ID, 2, expires)
	if err != nil {
		t.Fatal(err)
	}
	if len(granted) != 2 {
		t.Fatalf("granted %d cards, want 2", len(granted))
	}
	for _, record := range granted {
		if record.ExpiresAt != expires {
			t.Errorf("expires_at = %d, want %d", record.ExpiresAt, expires)
		}
	}

	if _, err := f.store.GrantUntil(context.Background(), person.ID, 1,
		time.Now().Add(-time.Minute).UnixMilli()); !errors.Is(err, ErrInvalidExpiry) {
		t.Errorf("past expiry gave %v, want ErrInvalidExpiry", err)
	}
}

// Automatic use spends the card that would otherwise disappear first, so a
// later-dated card is not wasted while an earlier one expires.
func TestSpendNextUsesTheEarliestExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "automatic")

	later, err := f.store.GrantUntil(ctx, person.ID, 1, time.Now().Add(60*24*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := f.store.GrantUntil(ctx, person.ID, 1, time.Now().Add(10*24*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SpendNext(ctx, nil, person.ID); err != nil {
		t.Fatal(err)
	}

	available, err := f.store.Available(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 || available[0].ID != later[0].ID {
		t.Errorf("available = %+v, want only later card %s; earlier was %s",
			available, later[0].ID, earlier[0].ID)
	}
}

// A card belongs to one account. Somebody else's id is answered the way a
// card that does not exist is, so the endpoint cannot be used to find out
// whether one does.
func TestACardCannotBeSpentByAnotherAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.reader(t, "owner")
	stranger := f.reader(t, "stranger")

	granted, err := f.store.Grant(ctx, nil, owner.ID, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Spend(ctx, stranger.ID, granted[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger spending it gave %v, want ErrNotFound", err)
	}

	cards, err := f.store.Available(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Errorf("the owner's card was taken: %+v", cards)
	}
}

// A batch is a stack of separate codes, not one code used many times, so no
// two of them may come out the same.
func TestABatchIsDistinctCodes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	minted, err := f.store.CreateCodes(ctx, CodeInput{Cards: 1, CardDays: 30}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(minted) != 60 {
		t.Fatalf("minted %d, want 60", len(minted))
	}

	seen := make(map[string]bool, len(minted))
	for _, code := range minted {
		if seen[code.Code] {
			t.Fatalf("%q was minted twice", code.Code)
		}
		seen[code.Code] = true
	}
}

// The alphabet leaves out the characters somebody would have to ask about:
// O against 0, I and L against 1. A code exists to be read off a screen and
// typed somewhere else, and every ambiguous glyph is a support message.
func TestGeneratedCodesAreUnambiguous(t *testing.T) {
	f := newFixture(t)

	minted, err := f.store.CreateCodes(context.Background(),
		CodeInput{Cards: 1, CardDays: 30}, 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range minted {
		if strings.ContainsAny(code.Code, "01OIL") {
			t.Errorf("%q contains a character that has to be guessed at", code.Code)
		}
	}
}

// A batch cannot be given a name: they would all be the same code, and only
// the first would exist.
func TestANamedBatchIsRefused(t *testing.T) {
	f := newFixture(t)

	_, err := f.store.CreateCodes(context.Background(),
		CodeInput{Code: "WELCOME", Cards: 1, CardDays: 30}, 5)
	if !errors.Is(err, ErrNamedBatch) {
		t.Errorf("naming a batch gave %v, want ErrNamedBatch", err)
	}

	// One is not a batch, so a name is exactly what it should take.
	minted, err := f.store.CreateCodes(context.Background(),
		CodeInput{Code: "WELCOME", Cards: 1, CardDays: 30}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(minted) != 1 || minted[0].Code != "WELCOME" {
		t.Errorf("minted %+v, want the name that was asked for", minted)
	}
}

// The panel needs the counts, not just the list. "None left" and "never had
// any" are different answers to why somebody cannot reset, and the available
// list alone reads the same for both.
func TestHeldSeparatesSpentFromExpiredFromLeft(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "holder")

	granted, err := f.store.Grant(ctx, nil, person.ID, 4, 30)
	if err != nil {
		t.Fatal(err)
	}
	// One spent.
	if err := f.store.Spend(ctx, person.ID, granted[0].ID); err != nil {
		t.Fatal(err)
	}
	// One expired without ever being used.
	if _, err := f.db.Exec(ctx, `UPDATE usage_cards SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), granted[1].ID); err != nil {
		t.Fatal(err)
	}

	held, err := f.store.Held(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held.Total != 4 || held.Available != 2 || held.Used != 1 || held.Expired != 1 {
		t.Errorf("held = %+v, want 4 total / 2 left / 1 used / 1 expired", held)
	}
	if len(held.Cards) != 2 {
		t.Errorf("listed %d cards, want the 2 that can still be spent", len(held.Cards))
	}
	// Soonest to expire first: that is the one somebody will ask about.
	if len(held.Cards) == 2 && held.Cards[0].ExpiresAt > held.Cards[1].ExpiresAt {
		t.Error("the list is not ordered by expiry")
	}

	// An account with nothing is zeros, not an error and not a nil list.
	empty, err := f.store.Held(ctx, f.reader(t, "nobody").ID)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || empty.Cards == nil || len(empty.Cards) != 0 {
		t.Errorf("an account with no cards gave %+v", empty)
	}
}

// 256 is not a multiple of the 31-symbol alphabet, so `int(b) % 31` makes the
// first eight symbols more likely than the rest. Those bytes are rejected
// instead, which is what these two draws check: one that has to skip them and
// still succeed, and one that has nothing left once they are gone.
func TestCodeSymbolsSkipTheSkewedTailOfTheByteRange(t *testing.T) {
	// 248 = 8*31, the largest multiple of 31 at or below 256, so 248..255 are
	// the bytes that cannot map evenly onto the alphabet.
	skewed := []byte{248, 249, 250, 251, 252, 253, 254, 255}

	if _, err := drawSymbols(bytes.NewReader(skewed), 4, 31); err == nil {
		t.Fatal("a stream of only rejected bytes produced symbols anyway")
	}

	// The same rejected tail, followed by bytes that are usable: the tail must
	// be skipped whole rather than folded in with a modulo.
	stream := append(append([]byte{}, skewed...), 0, 1, 2, 3)
	picked, err := drawSymbols(bytes.NewReader(stream), 4, 31)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0, 1, 2, 3}; !bytes.Equal(picked, want) {
		t.Fatalf("picked %v, want %v — the rejected bytes were not skipped", picked, want)
	}
}

// An expired card is the one an operator is most often asked to move, so
// rescheduling has to reach past the "available" list rather than only over
// it. A spent card is not moved: the reset it paid for already happened, and
// giving it a future date would hand it back.
func TestReschedulingMovesUnusedCardsIncludingLapsedOnes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "extended")

	granted, err := f.store.Grant(ctx, nil, person.ID, 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Spend(ctx, person.ID, granted[0].ID); err != nil {
		t.Fatal(err)
	}
	spentAt := cardRow(t, f, granted[0].ID).ExpiresAt
	if _, err := f.db.Exec(ctx, `UPDATE usage_cards SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), granted[1].ID); err != nil {
		t.Fatal(err)
	}

	target := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second).UnixMilli()
	moved, err := f.store.Reschedule(ctx, person.ID, nil, target)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 2 {
		t.Errorf("moved %d cards, want the 2 unused ones", moved)
	}
	for _, record := range granted[1:] {
		if got := cardRow(t, f, record.ID).ExpiresAt; got != target {
			t.Errorf("card %s expires at %d, want %d", record.ID, got, target)
		}
	}
	if got := cardRow(t, f, granted[0].ID).ExpiresAt; got != spentAt {
		t.Errorf("a spent card was moved to %d, want it left at %d", got, spentAt)
	}

	// The lapsed one is spendable again, which is the entire point of the
	// request that brings an operator here.
	left, err := f.store.Available(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Errorf("%d cards available after the move, want 2", len(left))
	}
}

// Naming cards moves those and leaves the rest, and no account can move
// another account's cards by naming their ids.
func TestReschedulingNamedCardsStaysInsideOneAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mine := f.reader(t, "owner")
	theirs := f.reader(t, "stranger")

	ours, err := f.store.Grant(ctx, nil, mine.ID, 2, 30)
	if err != nil {
		t.Fatal(err)
	}
	others, err := f.store.Grant(ctx, nil, theirs.ID, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	untouched := cardRow(t, f, others[0].ID).ExpiresAt

	target := time.Now().Add(90 * 24 * time.Hour).Truncate(time.Second).UnixMilli()
	moved, err := f.store.Reschedule(ctx, mine.ID, []string{ours[0].ID, others[0].ID}, target)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Errorf("moved %d cards, want only the one that belongs to this account", moved)
	}
	if got := cardRow(t, f, ours[0].ID).ExpiresAt; got != target {
		t.Errorf("the named card expires at %d, want %d", got, target)
	}
	if got := cardRow(t, f, ours[1].ID).ExpiresAt; got == target {
		t.Error("a card that was not named was moved as well")
	}
	if got := cardRow(t, f, others[0].ID).ExpiresAt; got != untouched {
		t.Errorf("another account's card moved to %d, want it left at %d", got, untouched)
	}
}

// The same window a hand-issued card is held to: a date in the past would
// retire every card it touched, and one a decade out is a typo nobody can
// undo a row at a time.
func TestReschedulingRefusesADateOutsideTheWindow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "misdated")
	if _, err := f.store.Grant(ctx, nil, person.ID, 1, 30); err != nil {
		t.Fatal(err)
	}

	past := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := f.store.Reschedule(ctx, person.ID, nil, past); !errors.Is(err, ErrInvalidExpiry) {
		t.Errorf("a past date gave %v, want ErrInvalidExpiry", err)
	}
	far := time.Now().Add(time.Duration(MaxDays+1) * 24 * time.Hour).UnixMilli()
	if _, err := f.store.Reschedule(ctx, person.ID, nil, far); !errors.Is(err, ErrInvalidExpiry) {
		t.Errorf("a date past the ceiling gave %v, want ErrInvalidExpiry", err)
	}
}

// Reads one card straight from the table, including the ones Available and
// Held deliberately hide.
func cardRow(t *testing.T, f *fixture, cardID string) Card {
	t.Helper()
	var record Card
	err := f.db.QueryRow(context.Background(),
		`SELECT id, source, expires_at, used_at, created_at FROM usage_cards WHERE id = ?`, cardID).
		Scan(&record.ID, &record.Source, &record.ExpiresAt, &record.UsedAt, &record.CreatedAt)
	if err != nil {
		t.Fatalf("read card %s: %v", cardID, err)
	}
	return record
}

// Taking a card back. The rule is the same one rescheduling follows: an
// unused card is an unspent permission and may be withdrawn; a spent one is
// the record of a reset that already happened, and deleting it would leave an
// account whose allowance was restored by nothing.
func TestRevokingTakesBackOnlyUnspentCards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "withdrawn")

	granted, err := f.store.Grant(ctx, nil, person.ID, 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Spend(ctx, person.ID, granted[0].ID); err != nil {
		t.Fatal(err)
	}
	// One lapsed, to prove revoking reaches past the available list the same
	// way rescheduling does.
	if _, err := f.db.Exec(ctx, `UPDATE usage_cards SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), granted[1].ID); err != nil {
		t.Fatal(err)
	}

	if err := f.store.Revoke(ctx, person.ID, granted[1].ID); err != nil {
		t.Errorf("revoking a lapsed card gave %v", err)
	}
	if err := f.store.Revoke(ctx, person.ID, granted[2].ID); err != nil {
		t.Errorf("revoking an available card gave %v", err)
	}
	if err := f.store.Revoke(ctx, person.ID, granted[0].ID); !errors.Is(err, ErrUsed) {
		t.Errorf("revoking a spent card gave %v, want ErrUsed", err)
	}

	held, err := f.store.Held(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held.Total != 1 || held.Used != 1 || held.Available != 0 {
		t.Errorf("held = %+v, want only the spent card left", held)
	}
}

// Naming somebody else's card id does not delete it, and an id that was
// never a card says so rather than reporting success.
func TestRevokingCannotReachAnotherAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mine := f.reader(t, "holder-a")
	theirs := f.reader(t, "holder-b")

	others, err := f.store.Grant(ctx, nil, theirs.ID, 1, 30)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.store.Revoke(ctx, mine.ID, others[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking across accounts gave %v, want ErrNotFound", err)
	}
	if err := f.store.Revoke(ctx, mine.ID, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking an unknown id gave %v, want ErrNotFound", err)
	}
	if cardRow(t, f, others[0].ID).ID != others[0].ID {
		t.Error("the other account's card is gone")
	}
}

func TestNamedCardsAndWindowVariants(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "variant-user")

	// 1. Grant named card with 5h window.
	granted5H, err := f.store.GrantNamed(ctx, nil, person.ID, 1, 30, "Five Hour Boost", []string{"5h"})
	if err != nil {
		t.Fatalf("grant 5h: %v", err)
	}
	if len(granted5H) != 1 {
		t.Fatalf("granted %d cards, want 1", len(granted5H))
	}
	if granted5H[0].Name != "Five Hour Boost" {
		t.Errorf("name = %q, want 'Five Hour Boost'", granted5H[0].Name)
	}
	if len(granted5H[0].Windows) != 1 || granted5H[0].Windows[0] != "5h" {
		t.Errorf("windows = %v, want ['5h']", granted5H[0].Windows)
	}

	// 2. Create code with combined windows (5h, 1w).
	codeRecord, err := f.store.CreateCode(ctx, CodeInput{
		Code:     "COMBO-WEEK",
		Name:     "Weekly Combo Card",
		Windows:  []string{"5h", "1w"},
		Cards:    5,
		CardDays: 14,
	})
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	if codeRecord.Name != "Weekly Combo Card" {
		t.Errorf("code name = %q, want 'Weekly Combo Card'", codeRecord.Name)
	}
	if len(codeRecord.Windows) != 2 || codeRecord.Windows[0] != "5h" || codeRecord.Windows[1] != "1w" {
		t.Errorf("code windows = %v, want ['5h', '1w']", codeRecord.Windows)
	}

	// 3. Redeem code and check minted card inherits name and windows.
	redeemedCard, err := f.store.Redeem(ctx, person.ID, "COMBO-WEEK")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if redeemedCard.Name != "Weekly Combo Card" {
		t.Errorf("redeemed name = %q, want 'Weekly Combo Card'", redeemedCard.Name)
	}
	if len(redeemedCard.Windows) != 2 || redeemedCard.Windows[0] != "5h" || redeemedCard.Windows[1] != "1w" {
		t.Errorf("redeemed windows = %v, want ['5h', '1w']", redeemedCard.Windows)
	}

	// 4. Check available cards list has both cards with their names and windows.
	available, err := f.store.Available(ctx, person.ID)
	if err != nil {
		t.Fatalf("available: %v", err)
	}
	if len(available) != 2 {
		t.Fatalf("available = %d cards, want 2", len(available))
	}

	// 5. SpendCard returns the card with its windows.
	spent, err := f.store.SpendCard(ctx, person.ID, redeemedCard.ID)
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	if spent.ID != redeemedCard.ID {
		t.Errorf("spent id = %q, want %q", spent.ID, redeemedCard.ID)
	}
	if spent.Name != "Weekly Combo Card" {
		t.Errorf("spent name = %q, want 'Weekly Combo Card'", spent.Name)
	}
	if len(spent.Windows) != 2 || spent.Windows[0] != "5h" || spent.Windows[1] != "1w" {
		t.Errorf("spent windows = %v, want ['5h', '1w']", spent.Windows)
	}
}

func TestSpendNextForWindowTargetsMatchingCards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "user-spend-window")

	// Grant three cards with different expiries:
	// 1. Expiring in 10 days: "1w" only
	// 2. Expiring in 20 days: "5h" only
	// 3. Expiring in 30 days: full reset (empty windows)
	cardsWeek, err := f.store.GrantNamed(ctx, nil, person.ID, 1, 10, "Week Card", []string{"1w"})
	if err != nil {
		t.Fatal(err)
	}
	cards5H, err := f.store.GrantNamed(ctx, nil, person.ID, 1, 20, "5H Card", []string{"5h"})
	if err != nil {
		t.Fatal(err)
	}
	cardsFull, err := f.store.GrantNamed(ctx, nil, person.ID, 1, 30, "Full Card", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Requesting "5h" should NOT pick the earliest expiring card ("Week Card" at 10 days).
	// It should pick "5H Card" (20 days).
	spent, err := f.store.SpendNextForWindow(ctx, nil, person.ID, "5h")
	if err != nil {
		t.Fatalf("spend next 5h: %v", err)
	}
	if spent.ID != cards5H[0].ID {
		t.Errorf("spent card id = %q, want 5H Card %q", spent.ID, cards5H[0].ID)
	}

	// Requesting "5h" again: "5H Card" is used. Full Card (30 days) covers 5h too!
	spent2, err := f.store.SpendNextForWindow(ctx, nil, person.ID, "5h")
	if err != nil {
		t.Fatalf("spend next 5h (fallback to full): %v", err)
	}
	if spent2.ID != cardsFull[0].ID {
		t.Errorf("spent card id = %q, want Full Card %q", spent2.ID, cardsFull[0].ID)
	}

	// Requesting "5h" a third time: Only "Week Card" remains, which does NOT cover 5h.
	_, err = f.store.SpendNextForWindow(ctx, nil, person.ID, "5h")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for 5h when only 1w card left, got: %v", err)
	}

	// But requesting "1w" succeeds and spends "Week Card":
	spent3, err := f.store.SpendNextForWindow(ctx, nil, person.ID, "1w")
	if err != nil {
		t.Fatalf("spend next 1w: %v", err)
	}
	if spent3.ID != cardsWeek[0].ID {
		t.Errorf("spent card id = %q, want Week Card %q", spent3.ID, cardsWeek[0].ID)
	}
}

func TestInvalidWindowsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "user-invalid-win")

	// 1. GrantNamed rejects invalid window
	_, err := f.store.GrantNamed(ctx, nil, person.ID, 1, 10, "Bad Card", []string{"invalid_window"})
	if !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("expected ErrInvalidWindow, got: %v", err)
	}

	// 2. GrantUntilNamed rejects invalid window
	now := time.Now().Add(24 * time.Hour).UnixMilli()
	_, err = f.store.GrantUntilNamed(ctx, person.ID, 1, now, "Bad Card", []string{"5hr"})
	if !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("expected ErrInvalidWindow, got: %v", err)
	}

	// 3. CreateCode rejects invalid window
	_, err = f.store.CreateCode(ctx, CodeInput{
		Code:    "BAD-WIN-CODE",
		Windows: []string{"weekly"},
		Cards:   1,
	})
	if !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("expected ErrInvalidWindow, got: %v", err)
	}

	// 4. CreateCodes rejects invalid window
	_, err = f.store.CreateCodes(ctx, CodeInput{
		Windows: []string{"xyz"},
		Cards:   1,
	}, 5)
	if !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("expected ErrInvalidWindow, got: %v", err)
	}
}
