package checkin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// progress is what an account has done, as the rules count it.
type progress struct {
	today        string
	checkedToday bool
	// The length of the streak the latest check-in belongs to, and whether it
	// is still going (the latest was today or yesterday).
	streak  int
	active  bool
	runFrom string
	// Days checked in this calendar month.
	monthDays  []int
	monthKey   string
	monthCount int
}

func (s *Service) progressOf(ctx context.Context, q database.Queryer, userID string, now time.Time) (progress, error) {
	loc := s.Location()
	p := progress{today: dayKey(now, loc)}
	yesterday := addDays(p.today, -1, loc)
	p.monthKey = p.today[:7]

	var day string
	var streak int
	err := q.QueryRow(ctx, `SELECT day, streak FROM checkins WHERE user_id = ? ORDER BY day DESC LIMIT 1`, userID).Scan(&day, &streak)
	switch {
	case err == nil:
		p.streak = streak
		p.checkedToday = day == p.today
		p.active = day == p.today || day == yesterday
		p.runFrom = addDays(day, -(streak - 1), loc)
	case !database.IsNotFound(err):
		return p, fmt.Errorf("checkin: read latest: %w", err)
	}

	rows, err := q.Query(ctx, `SELECT day FROM checkins WHERE user_id = ? AND day >= ? AND day <= ? ORDER BY day`,
		userID, p.monthKey+"-01", p.monthKey+"-31")
	if err != nil {
		return p, fmt.Errorf("checkin: read month: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return p, err
		}
		n, _ := strconv.Atoi(d[8:])
		p.monthDays = append(p.monthDays, n)
	}
	p.monthCount = len(p.monthDays)
	return p, rows.Err()
}

// periodFor is what a milestone is counted against right now: how far the
// account has got, and the key its claim is recorded under.
func (p progress) periodFor(r Rule) (done int, period string) {
	if r.Basis == BasisMonth {
		return p.monthCount, p.monthKey
	}
	return p.streak, p.runFrom
}

// RuleStatus is a milestone as the account sees it.
type RuleStatus struct {
	Rule
	Progress  int  `json:"progress"`
	Claimable bool `json:"claimable"`
	Claimed   bool `json:"claimed"`
}

// Status is the account's check-in page.
type Status struct {
	Enabled        bool         `json:"enabled"`
	Today          string       `json:"today"`
	CheckedInToday bool         `json:"checked_in_today"`
	Streak         int          `json:"streak"`
	MonthDays      []int        `json:"month_days"`
	MonthCount     int          `json:"month_count"`
	Daily          Reward       `json:"daily"`
	Rules          []RuleStatus `json:"rules"`
}

// StatusOf reads the page.
func (s *Service) StatusOf(ctx context.Context, userID string) (Status, error) {
	st := Status{Enabled: s.Enabled(), MonthDays: []int{}, Rules: []RuleStatus{}}
	if !st.Enabled {
		return st, nil
	}
	p, err := s.progressOf(ctx, s.db, userID, s.Now())
	if err != nil {
		return st, err
	}
	cfg := s.Config()
	st.Today, st.CheckedInToday, st.MonthCount, st.Daily = p.today, p.checkedToday, p.monthCount, cfg.Daily
	if p.active {
		st.Streak = p.streak
	}
	if p.monthDays != nil {
		st.MonthDays = p.monthDays
	}

	claimed := map[string]bool{}
	rows, err := s.db.Query(ctx, `SELECT rule_id, period FROM checkin_claims WHERE user_id = ?`, userID)
	if err != nil {
		return st, fmt.Errorf("checkin: read claims: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rule, period string
		if err := rows.Scan(&rule, &period); err != nil {
			return st, err
		}
		claimed[rule+"|"+period] = true
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, rule := range cfg.Rules {
		done, period := p.periodFor(rule)
		rs := RuleStatus{Rule: rule, Progress: min(done, rule.Days)}
		rs.Claimed = period != "" && claimed[rule.ID+"|"+period]
		rs.Claimable = done >= rule.Days && period != "" && !rs.Claimed
		st.Rules = append(st.Rules, rs)
	}
	return st, nil
}

// Result is what pressing the button did.
type Result struct {
	Day    string `json:"day"`
	Streak int    `json:"streak"`
	// What was given for the day, if anything.
	Reward Reward `json:"reward"`
	// The reward was meant to be given and could not be — the bar it names is
	// gone — and the check-in stands without it.
	RewardFailed bool `json:"reward_failed"`
}

// rewardError marks a failure to pay the reward, as opposed to one in
// recording the check-in.
type rewardError struct{ err error }

func (e rewardError) Error() string { return e.err.Error() }
func (e rewardError) Unwrap() error { return e.err }

// CheckIn records today for an account and pays the daily reward.
//
// The day's row is inserted with "do nothing on conflict" and the rows
// affected say whether it was this call that made it, so two presses arriving
// together record one day and pay one reward.
func (s *Service) CheckIn(ctx context.Context, userID string) (Result, error) {
	if !s.Enabled() {
		return Result{}, ErrDisabled
	}
	cfg := s.Config()
	attempt := func(pay bool) (Result, error) {
		now := s.Now()
		loc := s.Location()
		res := Result{Day: dayKey(now, loc)}
		err := s.db.Tx(ctx, func(tx *database.Tx) error {
			var prev int
			err := tx.QueryRow(ctx, `SELECT streak FROM checkins WHERE user_id = ? AND day = ?`,
				userID, addDays(res.Day, -1, loc)).Scan(&prev)
			if err != nil && !database.IsNotFound(err) {
				return fmt.Errorf("checkin: read yesterday: %w", err)
			}
			res.Streak = prev + 1
			done, err := tx.Exec(ctx, `INSERT INTO checkins (user_id, day, streak, created_at) VALUES (?, ?, ?, ?)
				ON CONFLICT (user_id, day) DO NOTHING`, userID, res.Day, res.Streak, now.UnixMilli())
			if err != nil {
				return fmt.Errorf("checkin: record: %w", err)
			}
			if n, _ := done.RowsAffected(); n == 0 {
				return ErrAlreadyToday
			}
			if pay && !cfg.Daily.none() {
				if err := s.give(ctx, tx, userID, cfg.Daily, "checkin", now); err != nil {
					return rewardError{err}
				}
				res.Reward = cfg.Daily
			}
			return nil
		})
		return res, err
	}
	res, err := attempt(true)
	var failed rewardError
	if errors.As(err, &failed) {
		slog.WarnContext(ctx, "checkin: the daily reward could not be paid; recording the check-in without it", "error", err)
		res, err = attempt(false)
		res.RewardFailed = true
	}
	return res, err
}

// Claim pays a milestone's reward, once for the period it was reached in.
func (s *Service) Claim(ctx context.Context, userID, ruleID string) (Reward, error) {
	if !s.Enabled() {
		return Reward{}, ErrDisabled
	}
	var rule *Rule
	for _, r := range s.Config().Rules {
		if r.ID == ruleID {
			r := r
			rule = &r
		}
	}
	if rule == nil {
		return Reward{}, ErrNoSuchRule
	}
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		now := s.Now()
		p, err := s.progressOf(ctx, tx, userID, now)
		if err != nil {
			return err
		}
		done, period := p.periodFor(*rule)
		if period == "" || done < rule.Days {
			return ErrNotReached
		}
		res, err := tx.Exec(ctx, `INSERT INTO checkin_claims (user_id, rule_id, period, claimed_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, rule_id, period) DO NOTHING`, userID, rule.ID, period, now.UnixMilli())
		if err != nil {
			return fmt.Errorf("checkin: record claim: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAlreadyClaimd
		}
		return s.give(ctx, tx, userID, rule.Reward, "checkin", now)
	})
	if err != nil {
		return Reward{}, err
	}
	return rule.Reward, nil
}
