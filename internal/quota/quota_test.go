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
		if _, err := service.Reserve(ctx, person, Estimate{}); err != nil {
			t.Fatalf("request %d was refused: %v", attempt, err)
		}
	}

	_, err := service.Reserve(ctx, person, Estimate{})
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

	if _, err := service.Reserve(ctx, account("user-1", ""), Estimate{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, account("user-2", ""), Estimate{}); err != nil {
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
	if _, err := service.Reserve(ctx, person, Estimate{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, person, Estimate{}); err == nil {
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
			if _, err := service.Reserve(ctx, person, Estimate{}); err == nil {
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
	if _, err := service.Reserve(ctx, person, Estimate{}); err != nil {
		t.Fatalf("the first request was refused with nothing spent: %v", err)
	}

	// A turn's cost is only known once it is over, so the ceiling is applied
	// to what has already been consumed.
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 1200, Credits: 1.2}); err != nil {
		t.Fatal(err)
	}

	_, err := service.Reserve(ctx, person, Estimate{})
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
	if _, err := service.Reserve(ctx, person, Estimate{}); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 0, Credits: 5.5}); err != nil {
		t.Fatal(err)
	}

	_, reserveErr := service.Reserve(ctx, person, Estimate{})
	exceeded, ok := AsExceeded(reserveErr)
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
		if _, err := service.Reserve(ctx, admin, Estimate{}); err != nil {
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
	if _, err := service.Reserve(ctx, person, Estimate{}); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 350, Credits: 0.35}); err != nil {
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
	if _, err := service.Reserve(ctx, person, Estimate{}); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 42, Credits: 0.042}); err != nil {
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

// An allowance runs from the account's own registration, so "when does this
// reset" is answered per account rather than by a calendar everybody shares.
func TestAllowanceWindowsRunFromRegistration(t *testing.T) {
	// Registered on a Thursday, mid-afternoon.
	anchor := time.Date(2026, 1, 15, 14, 30, 0, 0, time.UTC).UnixMilli()

	// Four days later: still inside the first week, which began at signup.
	now := time.Date(2026, 1, 19, 9, 0, 0, 0, time.UTC)
	week := time.UnixMilli(bucketStart(WindowWeek, now, anchor)).UTC()
	if week.UnixMilli() != anchor {
		t.Errorf("week start = %v, want the moment of registration", week)
	}
	if end := bucketEnd(WindowWeek, now, anchor); end.Day() != 22 {
		t.Errorf("week end = %v, want 22 January", end)
	}

	// Nine days later: the second week, still starting at the same time of
	// day rather than at midnight on a Monday.
	now = time.Date(2026, 1, 24, 9, 0, 0, 0, time.UTC)
	week = time.UnixMilli(bucketStart(WindowWeek, now, anchor)).UTC()
	if week.Day() != 22 || week.Hour() != 14 || week.Minute() != 30 {
		t.Errorf("second week start = %v, want 22 January 14:30", week)
	}

	// The month renews on the 15th, not the 1st.
	now = time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	month := time.UnixMilli(bucketStart(WindowMonth, now, anchor)).UTC()
	if month.Month() != time.February || month.Day() != 15 {
		t.Errorf("month start = %v, want 15 February", month)
	}
	if end := bucketEnd(WindowMonth, now, anchor); end.Month() != time.March || end.Day() != 15 {
		t.Errorf("month end = %v, want 15 March", end)
	}

	// The rate windows are still the wall clock's: a minute is a minute, and
	// the anchor has nothing to say about it.
	minute := time.UnixMilli(bucketStart(WindowRPM, now, anchor)).UTC()
	if minute.Second() != 0 || minute.Minute() != 0 || minute.Hour() != 8 {
		t.Errorf("rate window start = %v, want 08:00:00", minute)
	}
}

// An account created on a day that not every month has renews on the last day
// of the short ones, rather than sliding forward into the next month and
// taking the renewal date with it.
func TestMonthlyWindowClampsToShortMonths(t *testing.T) {
	anchor := time.Date(2026, 1, 31, 6, 0, 0, 0, time.UTC).UnixMilli()

	now := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
	start := time.UnixMilli(bucketStart(WindowMonth, now, anchor)).UTC()
	if start.Month() != time.February || start.Day() != 28 {
		t.Errorf("month start = %v, want 28 February", start)
	}

	// March has a 31st again, so the renewal comes back to it rather than
	// staying on the 28th for good.
	now = time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	start = time.UnixMilli(bucketStart(WindowMonth, now, anchor)).UTC()
	if start.Month() != time.March || start.Day() != 31 {
		t.Errorf("month start = %v, want 31 March", start)
	}
}

// Two accounts registered a fortnight apart never share a bucket, which is
// the whole point: one running out does not tell the other anything, and
// they do not all come back at midnight together.
func TestTwoAccountsGetDifferentWindows(t *testing.T) {
	// Not a whole number of weeks apart, or the two would align again by
	// arithmetic and this would be testing nothing.
	early := time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC).UnixMilli()
	late := time.Date(2026, 1, 18, 16, 0, 0, 0, time.UTC).UnixMilli()
	now := time.Date(2026, 2, 20, 15, 0, 0, 0, time.UTC)

	if bucketStart(WindowWeek, now, early) == bucketStart(WindowWeek, now, late) {
		t.Error("two accounts registered a fortnight apart share a week bucket")
	}
	if bucketStart(WindowMonth, now, early) == bucketStart(WindowMonth, now, late) {
		t.Error("two accounts registered a fortnight apart share a month bucket")
	}
}

func TestPruneRemovesRolledOverBuckets(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()

	if err := service.Settle(ctx, account("user-1", ""), Estimate{}, Estimate{Tokens: 10, Credits: 0.01}); err != nil {
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

// --- reservations ------------------------------------------------------------

// The hole this was written for: charging nothing until a turn finished meant
// concurrent turns all saw the same untouched counter and all passed. The
// allowance was only ever enforced against turns that had already ended.
func TestConcurrentTurnsCannotOverspendTokens(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	const limit = 10000
	const perTurn = 4000
	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, nil, ptrInt(limit), nil)},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	estimate := Estimate{Tokens: perTurn}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.Reserve(ctx, person, estimate); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Three fit; the fourth would have taken the total past the limit.
	if accepted != limit/perTurn {
		t.Fatalf("%d turns of %d tokens were accepted against a limit of %d",
			accepted, perTurn, limit)
	}
}

// A reservation is a hold, not a charge: what the turn did not spend has to
// come back, or a generous ceiling silently becomes the price of every turn.
func TestReleaseGivesBackTheReservation(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, nil, ptrInt(10000), nil)},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	reserved, err := service.Reserve(ctx, person, Estimate{Tokens: 4000, Credits: 4})
	if err != nil {
		t.Fatal(err)
	}

	// What it really cost, then the hold coming back. Both are deltas, so the
	// order genuinely does not matter — which is why the gateway can settle
	// from one goroutine and release from another.
	actual := Estimate{Tokens: 120, Credits: 0.12}
	if err := service.Settle(ctx, person, Estimate{}, actual); err != nil {
		t.Fatal(err)
	}
	if err := service.Release(ctx, person.ID, reserved); err != nil {
		t.Fatal(err)
	}

	var tokens int64
	var credits float64
	if err := db.QueryRow(ctx,
		`SELECT tokens, credits FROM usage_counters WHERE scope_key = ? AND window_kind = ?`,
		"u:user-1", Window5H).Scan(&tokens, &credits); err != nil {
		t.Fatal(err)
	}
	if tokens != actual.Tokens {
		t.Errorf("tokens = %d, want the %d actually spent", tokens, actual.Tokens)
	}
	if credits < actual.Credits-0.001 || credits > actual.Credits+0.001 {
		t.Errorf("credits = %v, want %v", credits, actual.Credits)
	}
}

// Two refunds racing must not drive a counter below zero and hand out free
// allowance.
func TestCountersNeverGoNegative(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()

	if _, err := service.Policies().Save(ctx, Policy{
		Scope:   ScopeGlobal,
		Windows: map[Window]Limits{Window5H: limits(true, nil, ptrInt(100000), nil)},
	}); err != nil {
		t.Fatal(err)
	}

	person := account("user-1", "")
	reserved, err := service.Reserve(ctx, person, Estimate{Tokens: 5000, Credits: 50})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 100, Credits: 1}); err != nil {
		t.Fatal(err)
	}
	// The same hold handed back twice, which is what a retry of the deferred
	// release amounts to.
	for range 2 {
		if err := service.Release(ctx, person.ID, reserved); err != nil {
			t.Fatal(err)
		}
	}

	var tokens int64
	var credits float64
	if err := db.QueryRow(ctx,
		`SELECT tokens, credits FROM usage_counters WHERE scope_key = ? AND window_kind = ?`,
		"u:user-1", Window5H).Scan(&tokens, &credits); err != nil {
		t.Fatal(err)
	}
	if tokens < 0 || credits < 0 {
		t.Fatalf("counter went negative: %d tokens, %v credits", tokens, credits)
	}
}

func TestConcurrencyCapIsPerAccount(t *testing.T) {
	service, _ := newService(t)

	releases := make([]func(), 0, MaxConcurrentPerUser)
	for i := 0; i < MaxConcurrentPerUser; i++ {
		release, err := service.Begin("user-1")
		if err != nil {
			t.Fatalf("slot %d of an allowed %d was refused", i+1, MaxConcurrentPerUser)
		}
		releases = append(releases, release)
	}

	if _, err := service.Begin("user-1"); err != ErrTooManyInFlight {
		t.Fatalf("err = %v, want ErrTooManyInFlight", err)
	}
	// Another account is unaffected.
	if _, err := service.Begin("user-2"); err != nil {
		t.Fatalf("a second account was blocked by the first: %v", err)
	}

	releases[0]()
	releases[0]() // idempotent: a deferred release may also be called early
	if _, err := service.Begin("user-1"); err != nil {
		t.Fatalf("a freed slot was not reusable: %v", err)
	}
}
