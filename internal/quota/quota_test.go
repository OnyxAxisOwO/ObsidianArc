package quota

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func newService(t *testing.T) (*Service, *database.DB) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "quota.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return NewService(db, NewStore(db), set), db
}

func account(id, groupID string) user.User {
	return user.User{ID: id, GroupID: groupID, Role: user.RoleUser, Status: user.StatusActive}
}

func ptrInt(value int64) *int64       { return &value }
func ptrFloat(value float64) *float64 { return &value }
func ptrBool(value bool) *bool        { return &value }

func limits(enabled bool, requests *int64, tokens *int64, credits *float64) Limits {
	return Limits{Enabled: ptrBool(enabled), Requests: requests, Tokens: tokens, Credits: credits}
}

// --- resolution ------------------------------------------------------------------

// The whole point of three levels: a user override wins over their group,
// which wins over the instance default, field by field rather than wholesale.
func TestResolveLayersFieldByField(t *testing.T) {
	global := Policy{RPM: ptrInt(60), Windows: map[Window]Limits{
		Window5H: limits(true, ptrInt(100), nil, nil),
	}}
	groupPolicy := Policy{Windows: map[Window]Limits{
		WindowMonth: limits(true, nil, ptrInt(1_000_000), nil),
	}}
	userPolicy := Policy{Windows: map[Window]Limits{
		Window5H: {Requests: ptrInt(500)},
	}}

	resolved := Resolve(global, groupPolicy, userPolicy)

	if resolved.RPM == nil || *resolved.RPM != 60 {
		t.Errorf("the global rate limit was not inherited: %v", resolved.RPM)
	}
	five := resolved.Windows[Window5H]
	if five.Enabled == nil || !*five.Enabled {
		t.Error("the user override cleared the window global had enabled")
	}
	if five.Requests == nil || *five.Requests != 500 {
		t.Errorf("the user's request limit did not win: %v", five.Requests)
	}
	month := resolved.Windows[WindowMonth]
	if month.Tokens == nil || *month.Tokens != 1_000_000 {
		t.Errorf("the group's monthly limit was lost: %v", month.Tokens)
	}
}

// An explicit `enabled = false` at a lower level is an exemption, not an
// absence: it must beat a window the level above turned on.
func TestExplicitDisableBeatsInheritedEnable(t *testing.T) {
	global := Policy{Windows: map[Window]Limits{Window5H: limits(true, ptrInt(10), nil, nil)}}
	exempt := Policy{Windows: map[Window]Limits{Window5H: {Enabled: ptrBool(false)}}}

	resolved := Resolve(global, exempt)
	if resolved.Windows[Window5H].isOn() {
		t.Fatal("an explicit disable did not turn the window off")
	}
	if !resolved.Unlimited() {
		t.Error("a policy with every window off is not reported as unlimited")
	}
}

// --- enforcement -------------------------------------------------------------------

func TestReserveStopsAtTheRequestLimit(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, ptrInt(3), nil, nil)},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	for attempt := 1; attempt <= 3; attempt++ {
		if err := service.Reserve(ctx, person); err != nil {
			t.Fatalf("request %d was refused: %v", attempt, err)
		}
	}

	err := service.Reserve(ctx, person)
	exceeded, ok := AsExceeded(err)
	if !ok {
		t.Fatalf("the fourth request was allowed: %v", err)
	}
	if exceeded.Window != Window5H || exceeded.Dimension != "requests" {
		t.Errorf("rejected by %s/%s", exceeded.Window, exceeded.Dimension)
	}
	if !exceeded.ResetsAt.After(time.Now()) {
		t.Errorf("reset time is not in the future: %v", exceeded.ResetsAt)
	}
}

// One account's usage must not count against another's.
func TestLimitsAreScopedToTheAccount(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, ptrInt(1), nil, nil)},
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.Reserve(ctx, account("user-1", "")); err != nil {
		t.Fatal(err)
	}
	if err := service.Reserve(ctx, account("user-2", "")); err != nil {
		t.Errorf("a second account was blocked by the first's usage: %v", err)
	}
}

// A refused reservation must leave no trace: otherwise a user who is over one
// limit silently burns their allowance on every other window too.
func TestRejectedReservationRollsBack(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope: ScopeGlobal,
		Windows: map[Window]Limits{
			Window5H:    limits(true, ptrInt(1), nil, nil),
			WindowMonth: limits(true, ptrInt(1000), nil, nil),
		},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	if err := service.Reserve(ctx, person); err != nil {
		t.Fatal(err)
	}
	if err := service.Reserve(ctx, person); err == nil {
		t.Fatal("the second request was allowed")
	}

	// The monthly counter was incremented before the five-hour window
	// refused; the transaction has to have taken it back.
	var monthly int64
	err := db.QueryRow(ctx,
		`SELECT requests FROM usage_counters WHERE scope_key = ? AND window_kind = ?`,
		"u:user-1", WindowMonth).Scan(&monthly)
	if err != nil {
		t.Fatalf("read monthly counter: %v", err)
	}
	if monthly != 1 {
		t.Errorf("monthly counter = %d, want 1 — the refused request was still counted", monthly)
	}
}

// The property the whole design exists for: the check happens after an atomic
// increment, so concurrent requests cannot both see the last free slot.
func TestConcurrentReservationsCannotOverspend(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	const limit = 20
	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, ptrInt(limit), nil, nil)},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	const attempts = 60

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := service.Reserve(ctx, person); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if accepted != limit {
		t.Fatalf("%d of %d concurrent requests were accepted against a limit of %d", accepted, attempts, limit)
	}
}

func TestTokenCeilingRefusesOnceSpent(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{WindowMonth: limits(true, nil, ptrInt(1000), nil)},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	if err := service.Reserve(ctx, person); err != nil {
		t.Fatalf("the first request was refused with nothing spent: %v", err)
	}

	// A turn's cost is only known once it is over, so the ceiling is applied
	// to what has already been consumed.
	if err := service.Settle(ctx, person.ID, 1200, 1.2); err != nil {
		t.Fatal(err)
	}

	err := service.Reserve(ctx, person)
	exceeded, ok := AsExceeded(err)
	if !ok {
		t.Fatalf("a request was allowed after the token allowance was spent: %v", err)
	}
	if exceeded.Dimension != "tokens" {
		t.Errorf("rejected by %s, want tokens", exceeded.Dimension)
	}
}

func TestCreditCeiling(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{WindowWeek: limits(true, nil, nil, ptrFloat(5))},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	if err := service.Reserve(ctx, person); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person.ID, 0, 5.5); err != nil {
		t.Fatal(err)
	}

	exceeded, ok := AsExceeded(service.Reserve(ctx, person))
	if !ok || exceeded.Dimension != "credits" {
		t.Fatalf("the credit ceiling did not apply: %+v", exceeded)
	}
}

// An operator locked out of their own instance has no way back in, so
// administrators are exempt unless the setting says otherwise.
func TestAdministratorsBypassByDefault(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, ptrInt(1), nil, nil)},
	}); err != nil {
		t.Fatal(err)
	}

	admin := user.User{ID: "admin-1", Role: user.RoleAdmin, Status: user.StatusActive}
	for attempt := 0; attempt < 5; attempt++ {
		if err := service.Reserve(ctx, admin); err != nil {
			t.Fatalf("an administrator was rate limited: %v", err)
		}
	}
}

// --- reporting -----------------------------------------------------------------------

func TestSummaryReportsEveryWindow(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, ptrInt(50), nil, nil)},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	if err := service.Reserve(ctx, person); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person.ID, 350, 0.35); err != nil {
		t.Fatal(err)
	}

	summary, err := service.SummaryFor(ctx, person)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Unlimited {
		t.Error("an account with a limit was reported as unlimited")
	}
	if len(summary.Windows) != len(AllowanceWindows) {
		t.Fatalf("reported %d windows, want %d", len(summary.Windows), len(AllowanceWindows))
	}

	var five *WindowUsage
	for i := range summary.Windows {
		if summary.Windows[i].Kind == Window5H {
			five = &summary.Windows[i]
		}
	}
	if five == nil || !five.Enforced {
		t.Fatalf("the five-hour window is missing or not enforced: %+v", summary.Windows)
	}
	if five.UsedRequests != 1 || five.UsedTokens != 350 {
		t.Errorf("five-hour usage = %+v", *five)
	}
	if five.ResetsAt <= time.Now().UnixMilli() {
		t.Error("the reset time is not in the future")
	}

	// Settling touches every window, so consumption is already there if a
	// limit is switched on tomorrow.
	for _, window := range summary.Windows {
		if window.UsedTokens != 350 {
			t.Errorf("%s recorded %d tokens, want 350", window.Kind, window.UsedTokens)
		}
	}
}

func TestUnenforcedAccountStillReportsUsage(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	person := account("user-1", "")
	if err := service.Reserve(ctx, person); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person.ID, 42, 0.042); err != nil {
		t.Fatal(err)
	}

	summary, err := service.SummaryFor(ctx, person)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Unlimited {
		t.Error("an account with no policy is not reported as unlimited")
	}
	for _, window := range summary.Windows {
		if window.Enforced {
			t.Errorf("%s is enforced with no policy set", window.Kind)
		}
		if window.UsedTokens != 42 {
			t.Errorf("%s recorded %d tokens", window.Kind, window.UsedTokens)
		}
	}
}

// Windows are aligned rather than rolling, so "when does this reset" has an
// answer the interface can show.
func TestWindowAlignment(t *testing.T) {
	// A Wednesday.
	now := time.Date(2026, 3, 18, 14, 37, 12, 0, time.UTC)

	week := time.UnixMilli(bucketStart(WindowWeek, now)).UTC()
	if week.Weekday() != time.Monday || week.Hour() != 0 {
		t.Errorf("the week starts at %v, want Monday 00:00", week)
	}
	if week.Day() != 16 {
		t.Errorf("week start = %v, want 16 March", week)
	}

	month := time.UnixMilli(bucketStart(WindowMonth, now)).UTC()
	if month.Day() != 1 || month.Month() != time.March {
		t.Errorf("month start = %v", month)
	}

	if end := bucketEnd(WindowMonth, now); end.Month() != time.April || end.Day() != 1 {
		t.Errorf("month end = %v", end)
	}
	if end := bucketEnd(WindowWeek, now); end.Day() != 23 {
		t.Errorf("week end = %v, want 23 March", end)
	}
}

func TestPruneRemovesRolledOverBuckets(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()

	if err := service.Settle(ctx, "user-1", 10, 0.01); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE usage_counters SET window_start = ?`,
		time.Now().AddDate(0, -6, 0).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	removed, err := service.PruneCounters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Error("no stale buckets were pruned")
	}
}
