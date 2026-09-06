package card

import (
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

	granted, err := f.store.Grant(ctx, person.ID, 1, 30)
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

// An expired card is not offered and cannot be spent. Both halves matter: the
// listing is what a reader sees, and the spend is what actually decides.
func TestAnExpiredCardIsNeitherOfferedNorSpent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "late")

	granted, err := f.store.Grant(ctx, person.ID, 1, 30)
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

// A card belongs to one account. Somebody else's id is answered the way a
// card that does not exist is, so the endpoint cannot be used to find out
// whether one does.
func TestACardCannotBeSpentByAnotherAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.reader(t, "owner")
	stranger := f.reader(t, "stranger")

	granted, err := f.store.Grant(ctx, owner.ID, 1, 30)
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
