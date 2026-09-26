package quota

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
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

// Deleting the counters alone restores the numbers but leaves every account
// on its old clock. The administrator's global reset is a new common starting
// point, so an account with two hours left gets a full five hours and seven
// days from the moment of the reset.
func TestResetAllRestartsAllowanceWindowsFromNow(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	person := account("user-1", "")
	person.CreatedAt = time.Now().Add(-48 * time.Hour).UnixMilli()
	if _, err := service.Policies().Save(ctx, Policy{
		Scope: ScopeGlobal,
		Windows: map[Window]Limits{
			Window5H:   limits(true, ptrInt(10), nil, nil),
			WindowWeek: limits(true, ptrInt(10), nil, nil),
		},
	}); err != nil {
		t.Fatal(err)
	}

	before, err := service.SummaryFor(ctx, person)
	if err != nil {
		t.Fatal(err)
	}
	assertRemainingNear(t, before, Window5H, 2*time.Hour)
	assertRemainingNear(t, before, WindowWeek, 5*24*time.Hour)

	if err := service.ResetAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, person, Estimate{}); err != nil {
		t.Fatal(err)
	}
	after, err := service.SummaryFor(ctx, person)
	if err != nil {
		t.Fatal(err)
	}
	assertRemainingNear(t, after, Window5H, 5*time.Hour)
	assertRemainingNear(t, after, WindowWeek, 7*24*time.Hour)
	for _, usage := range after.Windows {
		if (usage.Kind == Window5H || usage.Kind == WindowWeek) && usage.UsedRequests != 1 {
			t.Errorf("%s recorded %d requests in the restarted window, want 1", usage.Kind, usage.UsedRequests)
		}
	}
}

func assertRemainingNear(t *testing.T, summary Summary, window Window, want time.Duration) {
	t.Helper()
	for _, usage := range summary.Windows {
		if usage.Kind != window {
			continue
		}
		remaining := time.Until(time.UnixMilli(usage.ResetsAt))
		if remaining < want-5*time.Second || remaining > want+5*time.Second {
			t.Errorf("%s remaining = %v, want about %v", window, remaining, want)
		}
		return
	}
	t.Errorf("summary has no %s window", window)
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

// Resetting a group used to name every member first, through a user listing
// whose page size is clamped to fifty above two hundred — so a large group had
// fifty accounts reset and was told it had all of them. The reset picks its own
// rows now, which is why this uses a group bigger than that clamp.
func TestResetGroupReachesEveryMemberOfALargeGroup(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()

	groups := group.NewStore(db)
	members, err := groups.Create(ctx, nil, group.CreateInput{Name: "Members", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	outsiders, err := groups.Create(ctx, nil, group.CreateInput{Name: "Outsiders"})
	if err != nil {
		t.Fatal(err)
	}

	users := user.NewStore(db)
	const size = 210
	inside := make([]user.User, 0, size)
	err = db.Tx(ctx, func(tx *database.Tx) error {
		for i := range size {
			record, err := users.Create(ctx, tx, user.CreateInput{
				Username:     fmt.Sprintf("member-%03d", i),
				PasswordHash: "x", GroupID: members.ID,
			})
			if err != nil {
				return err
			}
			inside = append(inside, record)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := users.Create(ctx, nil, user.CreateInput{
		Username: "stranger", PasswordHash: "x", GroupID: outsiders.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, member := range append(append([]user.User{}, inside...), stranger) {
		if err := service.Settle(ctx, member, Estimate{}, Estimate{Tokens: 100, Credits: 0.1}); err != nil {
			t.Fatal(err)
		}
	}

	if err := service.ResetGroup(ctx, members.ID); err != nil {
		t.Fatal(err)
	}

	for _, member := range inside {
		if used := spentThisMonth(t, service, member.ID); used != 0 {
			t.Fatalf("%s still shows %v spent after the group was reset", member.Username, used)
		}
	}
	if used := spentThisMonth(t, service, stranger.ID); used == 0 {
		t.Error("resetting one group cleared an account outside it")
	}
}

func TestResetWindowsClearsSpecificWindowsOnly(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	spender := account("user-variant", "")
	if err := service.Settle(ctx, spender, Estimate{}, Estimate{Tokens: 1000, Credits: 1.0}); err != nil {
		t.Fatal(err)
	}

	spent5H := spentInWindow(t, service, spender.ID, Window5H)
	spent1W := spentInWindow(t, service, spender.ID, WindowWeek)
	spent1M := spentInWindow(t, service, spender.ID, WindowMonth)
	if spent5H == 0 || spent1W == 0 || spent1M == 0 {
		t.Fatal("counters were not populated in all windows")
	}

	// Reset only 5h.
	if err := service.ResetWindows(ctx, []string{spender.ID}, []string{"5h"}); err != nil {
		t.Fatal(err)
	}

	if got := spentInWindow(t, service, spender.ID, Window5H); got != 0 {
		t.Errorf("5h window was not reset, got %v", got)
	}
	if got := spentInWindow(t, service, spender.ID, WindowWeek); got == 0 {
		t.Error("1w window was cleared unexpectedly")
	}
	if got := spentInWindow(t, service, spender.ID, WindowMonth); got == 0 {
		t.Error("1m window was cleared unexpectedly")
	}

	// Reset combined 1w and 1m.
	if err := service.ResetWindows(ctx, []string{spender.ID}, []string{"1w", "1m"}); err != nil {
		t.Fatal(err)
	}
	if got := spentInWindow(t, service, spender.ID, WindowWeek); got != 0 {
		t.Errorf("1w window was not reset, got %v", got)
	}
	if got := spentInWindow(t, service, spender.ID, WindowMonth); got != 0 {
		t.Errorf("1m window was not reset, got %v", got)
	}
}

func spentInWindow(t *testing.T, service *Service, userID string, window Window) float64 {
	t.Helper()
	var credits float64
	err := service.db.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(credits), 0) FROM usage_counters WHERE scope_key = ? AND window_kind = ?`,
		scopeKey(userID), window).Scan(&credits)
	if err != nil {
		t.Fatalf("read counter window %s: %v", window, err)
	}
	return credits
}
