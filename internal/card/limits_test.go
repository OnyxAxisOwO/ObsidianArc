package card

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A grant over the limit is refused rather than cut to it. The answer names the
// cards given, and a grant that stopped short would be reported as the number
// asked for, which is what the admin screen did.
func TestAGrantOverTheLimitIsRefusedNotCut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "overgrant")

	if _, err := f.store.Grant(ctx, nil, person.ID, MaxCards+1, 30); !errors.Is(err, ErrTooManyCards) {
		t.Fatalf("a grant of %d gave %v, want ErrTooManyCards", MaxCards+1, err)
	}
	if held, err := f.store.Held(ctx, person.ID); err != nil || held.Total != 0 {
		t.Errorf("a refused grant left %+v (%v) behind, want no cards", held, err)
	}
}

// Rescheduling a list past the limit is refused as a whole, so that no card
// on it moves while the request reports failure.
func TestReschedulingOverTheLimitMovesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	person := f.reader(t, "overmove")
	granted, err := f.store.Grant(ctx, nil, person.ID, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	before := cardRow(t, f, granted[0].ID)

	ids := make([]string, MaxCards+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("not-a-card-%d", i)
	}
	ids[0] = granted[0].ID
	expiresAt := time.Now().Add(90 * 24 * time.Hour).UnixMilli()
	if _, err := f.store.Reschedule(ctx, person.ID, ids, expiresAt); !errors.Is(err, ErrTooManyCards) {
		t.Fatalf("rescheduling %d cards gave %v, want ErrTooManyCards", len(ids), err)
	}
	if after := cardRow(t, f, granted[0].ID); after.ExpiresAt != before.ExpiresAt {
		t.Errorf("a refused reschedule moved the card from %d to %d", before.ExpiresAt, after.ExpiresAt)
	}
}

// A code over the limit is refused, the same as a batch over MaxBatch: the
// operator is told why, and no code is written.
func TestACodeOverTheCardLimitIsRefusedNotCut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, err := f.store.CreateCodes(ctx, CodeInput{Cards: MaxCards + 1, CardDays: 30}, 1)
	if !errors.Is(err, ErrTooManyCards) {
		t.Fatalf("a code of %d cards gave %v, want ErrTooManyCards", MaxCards+1, err)
	}
	if codes, _ := f.store.ListCodes(ctx); len(codes) != 0 {
		t.Errorf("a refused code left %d codes behind", len(codes))
	}
}

// Two claw-backs against one account at once must not read the same cards. Each
// takes its share, so together they take what both asked for. Without the
// owner's row lock they race: both read the same cards, the first deletes them,
// and the second takes nothing while cards it asked for are still there.
func TestConcurrentClawBacksTakeDifferentCards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for round := 0; round < 25; round++ {
		person := f.reader(t, fmt.Sprintf("clawback%d", round))
		if _, err := f.store.Grant(ctx, nil, person.ID, 4, 30); err != nil {
			t.Fatal(err)
		}

		var (
			wg      sync.WaitGroup
			start   = make(chan struct{})
			revoked [2]int
			errs    [2]error
		)
		for i := range revoked {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				revoked[i], errs[i] = f.store.RevokeAvailable(ctx, nil, person.ID, 2)
			}()
		}
		close(start)
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Fatalf("round %d: a claw-back failed: %v", round, err)
			}
		}
		if total := revoked[0] + revoked[1]; total != 4 {
			t.Fatalf("round %d: two claw-backs of two cards took %d, want 4", round, total)
		}
	}
}
