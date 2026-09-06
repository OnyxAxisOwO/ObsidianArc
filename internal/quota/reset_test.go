package quota

import (
	"context"
	"fmt"
	"testing"
)

// A reset puts the allowance back to full without touching what the ledger
// says was spent.
func TestResetClearsOneAccountOnly(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	spender := account("user-1", "")
	bystander := account("user-2", "")

	if err := service.Settle(ctx, spender, Estimate{}, Estimate{Tokens: 900, Credits: 0.9}); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, bystander, Estimate{}, Estimate{Tokens: 400, Credits: 0.4}); err != nil {
		t.Fatal(err)
	}

	if used := spentThisMonth(t, service, spender.ID); used == 0 {
		t.Fatal("nothing was counted against the spender to begin with")
	}

	if err := service.Reset(ctx, []string{spender.ID}); err != nil {
		t.Fatal(err)
	}

	if used := spentThisMonth(t, service, spender.ID); used != 0 {
		t.Errorf("the reset account still shows %v spent", used)
	}
	if used := spentThisMonth(t, service, bystander.ID); used == 0 {
		t.Error("resetting one account cleared another's counters")
	}
}

func TestResetAllClearsEveryone(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	people := []string{"user-1", "user-2", "user-3"}
	for _, id := range people {
		if err := service.Settle(ctx, account(id, ""), Estimate{}, Estimate{Tokens: 500, Credits: 0.5}); err != nil {
			t.Fatal(err)
		}
	}

	if err := service.ResetAll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range people {
		if used := spentThisMonth(t, service, id); used != 0 {
			t.Errorf("%s still shows %v spent", id, used)
		}
	}
}

// Resetting nobody is not an error and must not become "reset everybody" —
// an empty IN list is the shape a mistyped scope would arrive as.
func TestResetOfNoAccountsTouchesNothing(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	person := account("user-1", "")
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 700, Credits: 0.7}); err != nil {
		t.Fatal(err)
	}
	if err := service.Reset(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if used := spentThisMonth(t, service, person.ID); used == 0 {
		t.Error("resetting an empty list cleared the counters anyway")
	}
}

// A member of a group larger than one batch is still reset: the store splits
// the delete rather than building one statement with every placeholder in it.
func TestResetSpansMoreThanOneBatch(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	ids := make([]string, 0, 450)
	for i := range 450 {
		id := fmt.Sprintf("user-%d", i)
		ids = append(ids, id)
		if err := service.Settle(ctx, account(id, ""), Estimate{}, Estimate{Tokens: 10, Credits: 0.01}); err != nil {
			t.Fatal(err)
		}
	}

	if err := service.Reset(ctx, ids); err != nil {
		t.Fatal(err)
	}
	// The first, one from the middle of the second batch, and the last: the
	// boundaries are where a chunked delete goes wrong.
	for _, at := range []int{0, 249, len(ids) - 1} {
		if used := spentThisMonth(t, service, ids[at]); used != 0 {
			t.Errorf("%s (index %d) still shows %v spent", ids[at], at, used)
		}
	}
}

func spentThisMonth(t *testing.T, service *Service, userID string) float64 {
	t.Helper()
	var credits float64
	err := service.db.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(credits), 0) FROM usage_counters WHERE scope_key = ?`,
		scopeKey(userID)).Scan(&credits)
	if err != nil {
		t.Fatalf("read counters: %v", err)
	}
	return credits
}
