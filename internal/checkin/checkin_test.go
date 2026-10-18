package checkin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	svc   *Service
	db    *database.DB
	bonus *bonus.Store
	cards *card.Store
	users *user.Store
	group string
	now   time.Time
	// The first day, 10:00 UTC, of a month that has not begun: grants expire
	// relative to the test's clock and are checked against the real one, so the
	// days it counts have to be ahead of it.
	base time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, dbtest.Either(t, filepath.Join(t.TempDir(), "checkin.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	g, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	real := time.Now().UTC()
	base := time.Date(real.Year(), real.Month()+1, 1, 10, 0, 0, 0, time.UTC)
	f := &fixture{db: db, bonus: bonus.NewStore(db), cards: card.NewStore(db), users: user.NewStore(db), group: g.ID,
		now: base, base: base}
	f.svc = NewService(db, set, f.bonus, f.cards)
	f.svc.Now = func() time.Time { return f.now }
	return f
}

func (f *fixture) person(t *testing.T, name string) user.User {
	t.Helper()
	u, err := f.users.Create(context.Background(), nil, user.CreateInput{
		Username: name, PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive, GroupID: f.group,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) configure(t *testing.T, s Settings) {
	t.Helper()
	if s.Timezone == "" {
		s.Timezone = "UTC"
	}
	if err := f.svc.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) bar(t *testing.T) bonus.Bar {
	t.Helper()
	bar, err := f.bonus.CreateBar(context.Background(), bonus.Bar{Name: "签到赠金", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	return bar
}

// at is the day-th of the test's month, 10:00 UTC.
func (f *fixture) at(day int) time.Time { return f.base.AddDate(0, 0, day-1) }

func (f *fixture) iso(day int) string { return f.at(day).Format("2006-01-02") }

func (f *fixture) checkInOn(t *testing.T, userID string, day int) (Result, error) {
	t.Helper()
	f.now = f.at(day)
	return f.svc.CheckIn(context.Background(), userID)
}

func (f *fixture) grants(t *testing.T, userID string) (n int, total float64) {
	t.Helper()
	if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM bonus_grants WHERE user_id = ?`, userID).Scan(&n, &total); err != nil {
		t.Fatal(err)
	}
	return n, total
}

func TestCheckInIsOffUntilTheAdministratorSwitchesItOn(t *testing.T) {
	f := newFixture(t)
	a := f.person(t, "alice")
	if _, err := f.svc.CheckIn(context.Background(), a.ID); !errors.Is(err, ErrDisabled) {
		t.Fatalf("check-in while off: %v", err)
	}
	if st, _ := f.svc.StatusOf(context.Background(), a.ID); st.Enabled {
		t.Fatal("the status says it is on")
	}
}

func TestOnceADayAndTheStreakCountsConsecutiveDays(t *testing.T) {
	f := newFixture(t)
	f.configure(t, Settings{Enabled: true})
	a := f.person(t, "alice")

	res, err := f.checkInOn(t, a.ID, 1)
	if err != nil || res.Streak != 1 || res.Day != f.iso(1) {
		t.Fatalf("first: %+v %v", res, err)
	}
	if _, err := f.checkInOn(t, a.ID, 1); !errors.Is(err, ErrAlreadyToday) {
		t.Fatalf("twice on one day: %v", err)
	}
	if res, _ := f.checkInOn(t, a.ID, 2); res.Streak != 2 {
		t.Fatalf("next day: %+v", res)
	}
	if res, _ := f.checkInOn(t, a.ID, 3); res.Streak != 3 {
		t.Fatalf("third day: %+v", res)
	}
	// A day missed starts again.
	if res, _ := f.checkInOn(t, a.ID, 5); res.Streak != 1 {
		t.Fatalf("after a gap: %+v", res)
	}
	st, err := f.svc.StatusOf(context.Background(), a.ID)
	if err != nil || st.Streak != 1 || !st.CheckedInToday || st.MonthCount != 4 || len(st.MonthDays) != 4 || st.MonthDays[3] != 5 {
		t.Fatalf("status: %+v %v", st, err)
	}
	// The streak is over once a day has passed with no check-in.
	f.now = f.at(7)
	if st, _ := f.svc.StatusOf(context.Background(), a.ID); st.Streak != 0 || st.CheckedInToday {
		t.Fatalf("a streak that ended still shows: %+v", st)
	}
}

// The day is the administrator's zone's, not the server's.
func TestADayIsCountedInTheConfiguredTimeZone(t *testing.T) {
	f := newFixture(t)
	f.configure(t, Settings{Enabled: true, Timezone: "Asia/Shanghai"})
	a := f.person(t, "alice")
	f.now = f.at(1).Add(10 * time.Hour) // 20:00 UTC, which is 04:00 on the 2nd in Shanghai
	res, err := f.svc.CheckIn(context.Background(), a.ID)
	if err != nil || res.Day != f.iso(2) {
		t.Fatalf("day = %q, %v", res.Day, err)
	}
	f.now = f.at(2).Add(-5 * time.Hour) // 05:00 UTC, 13:00 the same Shanghai day
	if _, err := f.svc.CheckIn(context.Background(), a.ID); !errors.Is(err, ErrAlreadyToday) {
		t.Fatalf("the same local day: %v", err)
	}
}

func TestPressesArrivingTogetherRecordOneDayAndPayOneReward(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Daily: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 2, ValidDays: 7}}})
	a := f.person(t, "alice")

	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.CheckIn(context.Background(), a.ID); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if !errors.Is(err, ErrAlreadyToday) {
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	n, total := f.grants(t, a.ID)
	if ok != 1 || n != 1 || total != 2 {
		t.Fatalf("%d presses succeeded and %d grants of %v were made", ok, n, total)
	}
}

func TestTheDailyRewardIsABonusGrantThatExpires(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Daily: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 0.5, ValidDays: 7}}})
	a := f.person(t, "alice")
	res, err := f.checkInOn(t, a.ID, 1)
	if err != nil || res.Reward.Kind != KindBonus || res.RewardFailed {
		t.Fatalf("%+v %v", res, err)
	}
	var amount float64
	var expires int64
	if err := f.db.QueryRow(context.Background(), `SELECT amount, expires_at FROM bonus_grants WHERE user_id = ? AND source = 'checkin'`, a.ID).Scan(&amount, &expires); err != nil {
		t.Fatal(err)
	}
	if amount != 0.5 || expires != f.at(8).UnixMilli() {
		t.Fatalf("grant %v expiring %v", amount, time.UnixMilli(expires).UTC())
	}
}

// A bar the administrator later deleted must not stop people checking in.
func TestACheckInStandsWhenItsRewardCannotBePaid(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Daily: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 1, ValidDays: 3}}})
	if err := f.bonus.DeleteBar(context.Background(), nil, bar.ID); err != nil {
		t.Fatal(err)
	}
	a := f.person(t, "alice")
	res, err := f.checkInOn(t, a.ID, 1)
	if err != nil || !res.RewardFailed || res.Streak != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := f.checkInOn(t, a.ID, 1); !errors.Is(err, ErrAlreadyToday) {
		t.Fatalf("it was not recorded: %v", err)
	}
}

// A daily reward that fails for a reason other than the bar being gone — here
// an amount no Save would accept, written past it — must not stand the check-in
// without its reward: the day would be recorded, and the reward could never be
// paid for it afterwards. The check-in rolls back so the day can be tried again.
func TestADailyRewardThatFailsForAnotherReasonRollsTheCheckInBack(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	raw, err := json.Marshal(Config{Daily: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 2_000_000, ValidDays: 3}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.svc.settings.SetMany(ctx, map[string]string{
		settings.CheckinEnabled:  "true",
		settings.CheckinTimezone: "UTC",
		settings.CheckinConfig:   string(raw),
	}); err != nil {
		t.Fatal(err)
	}
	a := f.person(t, "alice")
	if _, err := f.checkInOn(t, a.ID, 1); err == nil || errors.Is(err, ErrAlreadyToday) {
		t.Fatalf("a failed grant stood the check-in without its reward: %v", err)
	}
	var recorded int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM checkins WHERE user_id = ?`, a.ID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatalf("the day was recorded %d time(s) despite the failure", recorded)
	}
}

// A milestone whose bar has been switched off or deleted is a reward the
// account can do nothing about. It is refused as a conflict, not answered as a
// server fault that is logged on every attempt.
func TestAMilestoneWhoseBarIsGoneIsRefusedAsAConflict(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Rules: []Rule{{
		ID: "week", Title: "week", Basis: BasisStreak, Days: 1,
		Reward: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 5, ValidDays: 3},
	}}}})
	a := f.person(t, "alice")
	if _, err := f.checkInOn(t, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.bonus.DeleteBar(context.Background(), nil, bar.ID); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Claim(context.Background(), a.ID, "week")
	if !errors.Is(err, ErrRewardUnavailable) {
		t.Fatalf("claim against a deleted bar: %v", err)
	}
	var apiErr *httpx.Error
	if answer := TranslateError(err); !errors.As(answer, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("answered %v, want a 409", answer)
	}
	if n, _ := f.grants(t, a.ID); n != 0 {
		t.Fatalf("%d grants were made for a reward that could not be paid", n)
	}
}

// Switching a bar off is the same break as deleting it for a milestone that
// names it: the grant is refused, and the account must be told the reward can
// no longer be paid, not sent a server fault on every press.
func TestAMilestoneWhoseBarIsSwitchedOffIsRefusedAsAConflict(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Rules: []Rule{{
		ID: "week", Title: "week", Basis: BasisStreak, Days: 1,
		Reward: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 5, ValidDays: 3},
	}}}})
	a := f.person(t, "alice")
	if _, err := f.checkInOn(t, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(context.Background(), `UPDATE bonus_bars SET active = ? WHERE id = ?`, false, bar.ID); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Claim(context.Background(), a.ID, "week")
	if !errors.Is(err, ErrRewardUnavailable) {
		t.Fatalf("claim against a switched-off bar: %v", err)
	}
	var apiErr *httpx.Error
	if answer := TranslateError(err); !errors.As(answer, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("answered %v, want a 409", answer)
	}
	if n, _ := f.grants(t, a.ID); n != 0 {
		t.Fatalf("%d grants were made for a reward that could not be paid", n)
	}
}

// A bar's own end date is what a reward with no days of its own inherits. One
// that has already passed used to fall through to "never expires", so the
// bar's last day turned into a grant that outlived it for good.
func TestABarThatHasEndedPaysNothingInsteadOfPayingForever(t *testing.T) {
	f := newFixture(t)
	ended, err := f.bonus.CreateBar(context.Background(), bonus.Bar{
		Name: "ended", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true,
		DefaultExpiresAt: f.base.Add(-time.Hour).UnixMilli(),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	f.configure(t, Settings{Enabled: true, Config: Config{
		Daily: Reward{Kind: KindBonus, BarID: ended.ID, Amount: 1},
		Rules: []Rule{{ID: "week", Title: "week", Basis: BasisStreak, Days: 1,
			Reward: Reward{Kind: KindBonus, BarID: ended.ID, Amount: 5}}},
	}})
	a := f.person(t, "alice")

	res, err := f.checkInOn(t, a.ID, 1)
	if err != nil || !res.RewardFailed || res.Streak != 1 {
		t.Fatalf("the check-in itself should stand without its reward: %+v %v", res, err)
	}
	if _, err := f.svc.Claim(context.Background(), a.ID, "week"); !errors.Is(err, bonus.ErrInvalidExpiry) {
		t.Fatalf("claiming from an ended bar = %v, want bonus.ErrInvalidExpiry", err)
	}
	if n, _ := f.grants(t, a.ID); n != 0 {
		t.Fatalf("%d grants were written from a bar that has ended", n)
	}
	// The refused claim rolled back with the grant, so it can be made once
	// the bar is fixed.
	var claims int
	if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM checkin_claims WHERE user_id = ?`, a.ID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 0 {
		t.Fatalf("%d claims were recorded for a reward that was never paid", claims)
	}
}

func TestARewardWithNoDaysInheritsTheBarsEndDateWhileItIsAhead(t *testing.T) {
	f := newFixture(t)
	end := f.at(20).UnixMilli()
	live, err := f.bonus.CreateBar(context.Background(), bonus.Bar{
		Name: "live", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true, DefaultExpiresAt: end,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	f.configure(t, Settings{Enabled: true, Config: Config{Daily: Reward{Kind: KindBonus, BarID: live.ID, Amount: 1}}})
	a := f.person(t, "alice")
	if res, err := f.checkInOn(t, a.ID, 1); err != nil || res.RewardFailed {
		t.Fatalf("%+v %v", res, err)
	}
	var expires int64
	if err := f.db.QueryRow(context.Background(), `SELECT expires_at FROM bonus_grants WHERE user_id = ?`, a.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires != end {
		t.Fatalf("grant expires at %v, want the bar's end %v", time.UnixMilli(expires).UTC(), time.UnixMilli(end).UTC())
	}
}

func TestAStreakMilestoneIsClaimedOnceForTheRunItWasReachedIn(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Rules: []Rule{{
		ID: "w", Title: "连签 3 天", Basis: BasisStreak, Days: 3,
		Reward: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 10, ValidDays: 30},
	}}}})
	a := f.person(t, "alice")
	ctx := context.Background()

	f.checkInOn(t, a.ID, 1)
	f.checkInOn(t, a.ID, 2)
	if _, err := f.svc.Claim(ctx, a.ID, "w"); !errors.Is(err, ErrNotReached) {
		t.Fatalf("before three days: %v", err)
	}
	f.checkInOn(t, a.ID, 3)
	if st, _ := f.svc.StatusOf(ctx, a.ID); !st.Rules[0].Claimable || st.Rules[0].Progress != 3 {
		t.Fatalf("status: %+v", st.Rules)
	}
	if _, err := f.svc.Claim(ctx, a.ID, "w"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Claim(ctx, a.ID, "w"); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("a second claim in the same run: %v", err)
	}
	if n, total := f.grants(t, a.ID); n != 1 || total != 10 {
		t.Fatalf("%d grants of %v", n, total)
	}
	if _, err := f.svc.Claim(ctx, a.ID, "nope"); !errors.Is(err, ErrNoSuchRule) {
		t.Fatalf("an unknown milestone: %v", err)
	}
	// A new run that reaches it again earns it again.
	for _, day := range []int{6, 7, 8} {
		f.checkInOn(t, a.ID, day)
	}
	if _, err := f.svc.Claim(ctx, a.ID, "w"); err != nil {
		t.Fatalf("a new run: %v", err)
	}
}

func TestAReachedMilestoneCanStillBeClaimedAfterTheStreakBreaks(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Rules: []Rule{{
		ID: "w", Title: "x", Basis: BasisStreak, Days: 2, Reward: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 1},
	}}}})
	a := f.person(t, "alice")
	f.checkInOn(t, a.ID, 1)
	f.checkInOn(t, a.ID, 2)
	f.now = f.at(10)
	if _, err := f.svc.Claim(context.Background(), a.ID, "w"); err != nil {
		t.Fatalf("claiming after the run ended: %v", err)
	}
}

func TestAMonthMilestoneCountsTheDaysOfTheMonthAndStartsOverNextMonth(t *testing.T) {
	f := newFixture(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Rules: []Rule{{
		ID: "m", Title: "本月 3 天", Basis: BasisMonth, Days: 3,
		Reward: Reward{Kind: KindCard, Name: "月卡", Windows: []string{"5h"}, Cards: 2, ValidDays: 30},
	}}}})
	a := f.person(t, "alice")
	ctx := context.Background()
	for _, day := range []int{1, 5, 9} {
		f.checkInOn(t, a.ID, day)
	}
	if _, err := f.svc.Claim(ctx, a.ID, "m"); err != nil {
		t.Fatal(err)
	}
	cards, err := f.cards.Available(ctx, a.ID)
	if err != nil || len(cards) != 2 || cards[0].Name != "月卡" {
		t.Fatalf("cards: %+v %v", cards, err)
	}
	if _, err := f.svc.Claim(ctx, a.ID, "m"); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second claim in the month: %v", err)
	}
	// Next month the count starts again.
	f.now = f.base.AddDate(0, 1, 0)
	f.svc.CheckIn(ctx, a.ID)
	if _, err := f.svc.Claim(ctx, a.ID, "m"); !errors.Is(err, ErrNotReached) {
		t.Fatalf("a new month: %v", err)
	}
}

func TestTwoClaimsAtOnceGiveOneReward(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	f.configure(t, Settings{Enabled: true, Config: Config{Rules: []Rule{{
		ID: "w", Title: "x", Basis: BasisStreak, Days: 1, Reward: Reward{Kind: KindBonus, BarID: bar.ID, Amount: 5},
	}}}})
	a := f.person(t, "alice")
	f.checkInOn(t, a.ID, 1)
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.Claim(context.Background(), a.ID, "w"); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if n, _ := f.grants(t, a.ID); ok != 1 || n != 1 {
		t.Fatalf("%d claims succeeded and made %d grants", ok, n)
	}
}

func TestSettingsAreCheckedWhenSavedNotWhenSomebodyPressesTheButton(t *testing.T) {
	f := newFixture(t)
	bar := f.bar(t)
	good := Reward{Kind: KindBonus, BarID: bar.ID, Amount: 1, ValidDays: 1}
	cases := map[string]Settings{
		"a time zone that does not exist":  {Timezone: "Mars/Olympus"},
		"a daily bonus with no bar":        {Timezone: "UTC", Config: Config{Daily: Reward{Kind: KindBonus, BarID: "nope", Amount: 1}}},
		"a daily bonus of nothing":         {Timezone: "UTC", Config: Config{Daily: Reward{Kind: KindBonus, BarID: bar.ID}}},
		"a reward of a kind nobody knows":  {Timezone: "UTC", Config: Config{Daily: Reward{Kind: "gold"}}},
		"a milestone with no id":           {Timezone: "UTC", Config: Config{Rules: []Rule{{Basis: BasisStreak, Days: 1, Reward: good}}}},
		"two milestones with one id":       {Timezone: "UTC", Config: Config{Rules: []Rule{{ID: "a", Basis: BasisStreak, Days: 1, Reward: good}, {ID: "a", Basis: BasisStreak, Days: 2, Reward: good}}}},
		"a basis it does not know":         {Timezone: "UTC", Config: Config{Rules: []Rule{{ID: "a", Basis: "week", Days: 1, Reward: good}}}},
		"more days than a month has":       {Timezone: "UTC", Config: Config{Rules: []Rule{{ID: "a", Basis: BasisMonth, Days: 32, Reward: good}}}},
		"a milestone that gives nothing":   {Timezone: "UTC", Config: Config{Rules: []Rule{{ID: "a", Basis: BasisStreak, Days: 1}}}},
		"a card reward that never expires": {Timezone: "UTC", Config: Config{Daily: Reward{Kind: KindCard, Cards: 1}}},
		"a card reward naming no such window": {Timezone: "UTC", Config: Config{Daily: Reward{
			Kind: KindCard, Cards: 1, ValidDays: 1, Windows: []string{"7d"}}}},
		"a card reward whose name cannot be kept": {Timezone: "UTC", Config: Config{Daily: Reward{
			Kind: KindCard, Cards: 1, ValidDays: 1, Name: strings.Repeat("名", card.MaxNameChars+1)}}},
	}
	for name, in := range cases {
		if err := f.svc.Save(context.Background(), in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.svc.Save(context.Background(), Settings{Enabled: true, Timezone: "UTC", Config: Config{Daily: good}}); err != nil {
		t.Fatalf("a good configuration: %v", err)
	}
	if got := f.svc.Current(); !got.Enabled || got.Daily.Amount != 1 {
		t.Fatalf("current = %+v", got)
	}
}

// A grant trims a card's name before it cuts it, so padding that the trim removes
// must not count toward the limit: a name that fits once trimmed is accepted.
// The name past the limit is still refused, padding or not.
func TestAPaddedCardNameThatFitsIsAccepted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reward := func(name string) Settings {
		return Settings{Enabled: true, Timezone: "UTC", Config: Config{Daily: Reward{
			Kind: KindCard, Cards: 1, ValidDays: 1, Name: name}}}
	}
	padded := "  " + strings.Repeat("名", card.MaxNameChars) + "  "
	if err := f.svc.Save(ctx, reward(padded)); err != nil {
		t.Fatalf("a padded name that fits once trimmed: %v", err)
	}
	tooLong := "  " + strings.Repeat("名", card.MaxNameChars+1) + "  "
	if err := f.svc.Save(ctx, reward(tooLong)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a padded name over the limit: err = %v, want ErrInvalid", err)
	}
}

// Location reads the zone trimmed, so Save must accept a padded name and store
// it trimmed; refusing it would tell the administrator a valid zone is wrong.
func TestAPaddedTimeZoneIsSavedTrimmed(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Save(context.Background(), Settings{Enabled: true, Timezone: " Asia/Shanghai "}); err != nil {
		t.Fatalf("a padded zone: %v", err)
	}
	if got := f.svc.Current().Timezone; got != "Asia/Shanghai" {
		t.Fatalf("stored zone = %q", got)
	}
	if loc := f.svc.Location(); loc.String() != "Asia/Shanghai" {
		t.Fatalf("location = %s", loc)
	}
	if err := f.svc.Save(context.Background(), Settings{Timezone: "   "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a blank zone: %v", err)
	}
}
