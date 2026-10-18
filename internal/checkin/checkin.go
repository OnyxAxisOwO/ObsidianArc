// Package checkin is the daily check-in: once a day an account presses a
// button, and days of doing so earn rewards — a small one each day if the
// administrator wants, and larger ones at milestones the account claims itself.
// docs/architecture/bonus-and-checkin.md has the rules.
package checkin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

const (
	KindNone  = ""
	KindBonus = "bonus"
	KindCard  = "card"

	BasisStreak = "streak"
	BasisMonth  = "month"
)

// Reward is what a check-in or a milestone gives: credits in a bonus bar, or
// reset cards.
type Reward struct {
	Kind string `json:"kind"`
	// A bonus reward: which bar, how much, and how many days it lasts (zero
	// takes the bar's own default expiry).
	BarID     string  `json:"bar_id,omitempty"`
	Amount    float64 `json:"amount,omitempty"`
	ValidDays int     `json:"valid_days,omitempty"`
	// A card reward: what the cards are called, which windows they reset, how
	// many, and (with ValidDays) how long they last.
	Name    string   `json:"name,omitempty"`
	Windows []string `json:"windows,omitempty"`
	Cards   int      `json:"cards,omitempty"`
}

func (r Reward) none() bool { return r.Kind == KindNone }

// Rule is one milestone: reach Days of Basis and the reward can be claimed.
type Rule struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// "streak": consecutive days ending today or yesterday. "month": days
	// checked in during the calendar month.
	Basis  string `json:"basis"`
	Days   int    `json:"days"`
	Reward Reward `json:"reward"`
}

// Config is the rewards.
type Config struct {
	Daily Reward `json:"daily"`
	Rules []Rule `json:"rules"`
}

var (
	ErrDisabled      = errors.New("checkin: check-in is switched off")
	ErrAlreadyToday  = errors.New("checkin: already checked in today")
	ErrNoSuchRule    = errors.New("checkin: no such milestone")
	ErrNotReached    = errors.New("checkin: the milestone has not been reached")
	ErrAlreadyClaimd = errors.New("checkin: that milestone was already claimed for this period")
	ErrInvalid       = errors.New("checkin: invalid configuration")
	// The reward a milestone names cannot be paid any more: its bar was
	// switched off or ended, or its card windows are gone. The claim is
	// refused, and nothing is recorded, so it can be tried again once the
	// administrator has put it right.
	ErrRewardUnavailable = errors.New("checkin: the reward can no longer be paid")
)

// rewardUnavailable is the set of grant failures that mean the reward itself
// has gone away, as opposed to a database or a bug that a retry might clear.
// Only these are allowed to stand a check-in without its reward; anything else
// rolls the check-in back so the same day can try again.
func rewardUnavailable(err error) bool {
	return errors.Is(err, bonus.ErrNotFound) ||
		errors.Is(err, bonus.ErrInvalidExpiry) ||
		errors.Is(err, card.ErrInvalidWindow)
}

// Limits on what a configuration may hold: nothing legitimate is beyond them.
const (
	maxRules     = 30
	maxCardCount = 20
	maxDays      = 3650
)

type Service struct {
	db       *database.DB
	settings *settings.Service
	bonus    *bonus.Store
	cards    *card.Store
	// Now is the clock, replaceable by tests.
	Now func() time.Time
}

func NewService(db *database.DB, set *settings.Service, bars *bonus.Store, cards *card.Store) *Service {
	return &Service{db: db, settings: set, bonus: bars, cards: cards, Now: time.Now}
}

func (s *Service) Enabled() bool { return s.settings.Bool(settings.CheckinEnabled) }

// Location is the zone a day is counted in. An unknown name falls back to UTC
// rather than failing every check-in for a typo.
func (s *Service) Location() *time.Location {
	name := strings.TrimSpace(s.settings.Get(settings.CheckinTimezone))
	if loc, err := time.LoadLocation(name); err == nil && name != "" {
		return loc
	}
	return time.UTC
}

// Config is the rewards as saved, empty when none were.
func (s *Service) Config() Config {
	return parseConfig(s.settings.Get(settings.CheckinConfig))
}

// StoredConfig is the rewards as the database holds them now, read through q.
// Config reads the settings cache, which a save fills only after its
// transaction commits, so a caller holding the instance lock must read here to
// see what the save that held the lock before it committed.
func (s *Service) StoredConfig(ctx context.Context, q database.Queryer) (Config, error) {
	var raw string
	err := q.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, settings.CheckinConfig).Scan(&raw)
	if err != nil && !database.IsNotFound(err) {
		return Config{}, fmt.Errorf("checkin: read rewards: %w", err)
	}
	return parseConfig(raw), nil
}

func parseConfig(raw string) Config {
	var c Config
	if raw = strings.TrimSpace(raw); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			slog.Warn("checkin: the saved rewards cannot be read; none are given", "error", err)
			return Config{}
		}
	}
	if c.Rules == nil {
		c.Rules = []Rule{}
	}
	return c
}

// Settings is what the administrator's page edits.
type Settings struct {
	Enabled  bool   `json:"enabled"`
	Timezone string `json:"timezone"`
	Config
}

// Current is the settings as they stand.
func (s *Service) Current() Settings {
	return Settings{Enabled: s.Enabled(), Timezone: s.settings.Get(settings.CheckinTimezone), Config: s.Config()}
}

// Save checks and stores the settings. Whatever is wrong with them is said
// here, where an administrator can fix it, rather than when an account presses
// the button.
func (s *Service) Save(ctx context.Context, in Settings) error {
	// Trimmed before it is checked, because Location reads it trimmed: a padded
	// name that was refused here would have been a valid zone everywhere else.
	in.Timezone = strings.TrimSpace(in.Timezone)
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
		return fmt.Errorf("%w: %q is not a time zone", ErrInvalid, in.Timezone)
	}
	if len(in.Rules) > maxRules {
		return fmt.Errorf("%w: at most %d milestones", ErrInvalid, maxRules)
	}
	seen := map[string]bool{}
	for i := range in.Rules {
		r := &in.Rules[i]
		r.ID = strings.TrimSpace(r.ID)
		r.Title = strings.TrimSpace(r.Title)
		if r.ID == "" || len(r.ID) > 40 || seen[r.ID] {
			return fmt.Errorf("%w: milestone %d needs an id that no other has", ErrInvalid, i+1)
		}
		seen[r.ID] = true
		if r.Basis != BasisStreak && r.Basis != BasisMonth {
			return fmt.Errorf("%w: milestone %q counts consecutive days or the month's days", ErrInvalid, r.ID)
		}
		limit := 366
		if r.Basis == BasisMonth {
			limit = 31
		}
		if r.Days < 1 || r.Days > limit {
			return fmt.Errorf("%w: milestone %q needs between 1 and %d days", ErrInvalid, r.ID, limit)
		}
		if r.Reward.none() {
			return fmt.Errorf("%w: milestone %q gives nothing", ErrInvalid, r.ID)
		}
	}
	raw, err := json.Marshal(in.Config)
	if err != nil {
		return err
	}
	values := map[string]string{
		settings.CheckinEnabled:  fmt.Sprint(in.Enabled),
		settings.CheckinTimezone: in.Timezone,
		settings.CheckinConfig:   string(raw),
	}
	// The bars named here are checked under the instance lock. A removal or a
	// switch-off takes that lock before it looks for rewards that name the bar,
	// so a check made before the lock could approve a bar that is removed before
	// this save commits.
	return s.settings.SetManyChecked(ctx, values, func(q database.Queryer) error {
		if err := s.checkReward(ctx, q, in.Daily, "the daily reward"); err != nil {
			return err
		}
		for _, r := range in.Rules {
			if err := s.checkReward(ctx, q, r.Reward, "milestone "+r.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// checkReward reads any bar it names through q, so inside a transaction it sees
// what that transaction would commit over, not the cache.
func (s *Service) checkReward(ctx context.Context, q database.Queryer, r Reward, what string) error {
	switch r.Kind {
	case KindNone:
		return nil
	case KindBonus:
		if r.Amount <= 0 || r.Amount > bonus.MaxAmount || r.ValidDays < 0 || r.ValidDays > maxDays {
			return fmt.Errorf("%w: %s needs an amount and a number of days it lasts", ErrInvalid, what)
		}
		bar, err := s.bonus.Bar(ctx, q, r.BarID)
		if err != nil || !bar.Active {
			return fmt.Errorf("%w: %s names a bonus bar that does not exist or is switched off", ErrInvalid, what)
		}
		if bar.Kind == bonus.KindShared {
			return fmt.Errorf("%w: %s names a shared pool, which is not granted to accounts", ErrInvalid, what)
		}
	case KindCard:
		if r.Cards < 1 || r.Cards > maxCardCount || r.ValidDays < 1 || r.ValidDays > maxDays {
			return fmt.Errorf("%w: %s needs a number of cards and the days they last", ErrInvalid, what)
		}
		// Checked here for the same reason as everything else in this function:
		// a window the card cannot reset fails every grant that names it, and
		// the failure would surface on an account's press, not the save.
		if err := card.CheckWindows(r.Windows); err != nil {
			return fmt.Errorf("%w: %s names a quota window that does not exist", ErrInvalid, what)
		}
		// A grant cuts a longer name down to MaxNameChars without a word, and the
		// settings blob that holds it is returned to every account, so it is
		// refused here while the administrator can still see why. The grant trims
		// the name before it cuts, so the length is measured the same way: padding
		// the trim removes must not get a name that fits refused.
		if utf8.RuneCountInString(strings.TrimSpace(r.Name)) > card.MaxNameChars {
			return fmt.Errorf("%w: %s has a name longer than %d characters", ErrInvalid, what, card.MaxNameChars)
		}
	default:
		return fmt.Errorf("%w: %s is neither a bonus nor a card", ErrInvalid, what)
	}
	return nil
}

// give pays a reward to an account inside the caller's transaction.
func (s *Service) give(ctx context.Context, tx database.Queryer, userID string, r Reward, source string, now time.Time) error {
	switch r.Kind {
	case KindNone:
		return nil
	case KindBonus:
		expires := int64(0)
		if r.ValidDays > 0 {
			expires = now.Add(time.Duration(r.ValidDays) * 24 * time.Hour).UnixMilli()
		} else {
			bar, err := s.bonus.Bar(ctx, tx, r.BarID)
			if err != nil {
				return fmt.Errorf("checkin: read bonus bar: %w", err)
			}
			// A date that has passed must not fall through to "never
			// expires": the bar ended, and a grant that outlives it forever
			// is the opposite of what the operator set. The admin grant path
			// refuses the same case, through the same error.
			if bar.DefaultExpiresAt != 0 && bar.DefaultExpiresAt <= now.UnixMilli() {
				return fmt.Errorf("checkin: bonus bar %q ended and can no longer be granted: %w",
					bar.Name, bonus.ErrInvalidExpiry)
			}
			expires = bar.DefaultExpiresAt
		}
		_, err := s.bonus.GrantTo(ctx, tx, r.BarID, userID, r.Amount, expires, source, "")
		return err
	case KindCard:
		_, err := s.cards.GrantNamed(ctx, tx, userID, r.Cards, r.ValidDays, r.Name, r.Windows)
		return err
	}
	return fmt.Errorf("%w: unknown reward %q", ErrInvalid, r.Kind)
}

func dayKey(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

func addDays(day string, n int, loc *time.Location) string {
	t, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}
