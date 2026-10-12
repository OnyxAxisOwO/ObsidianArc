package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Registration controls: who may hold an account here, and how fast accounts
// may appear.
//
// Both exist for the same reason — an open instance is a free inference
// endpoint the moment someone writes a script against the sign-up form — and
// both are off by default, because an instance with registration closed
// already needs neither.

// SignupThrottleError says how long until the next account may be created.
// A refusal rather than a queue: holding the connection open would tie up a
// request for minutes and tell the caller nothing they cannot be told now.
type SignupThrottleError struct{ RetryAfter time.Duration }

func (e *SignupThrottleError) Error() string {
	return fmt.Sprintf("too many accounts created; try again in %s", e.RetryAfter.Round(time.Second))
}

// EmailDomainError names what would have been acceptable, because "your email
// was rejected" without saying which are allowed is an unanswerable error.
type EmailDomainError struct{ Allowed []string }

func (e *EmailDomainError) Error() string {
	return "email address must be at " + strings.Join(e.Allowed, ", ")
}

// signupGate counts accounts created in the last minute and the last hour, so a
// request the limit is going to refuse is refused before the paid review runs.
// It is only a fast refusal: it is kept per process, so it never grants an
// allowance, and the count that decides is the database's (checkSignupRate),
// which every process sharing the database writes to and reads from.
//
// Instance-wide rather than per address: the abuse this is for is a script
// making accounts, and a script that is worth throttling has more than one
// address. The fast gate stays in memory because a request that the database
// would refuse anyway is cheaper to turn away without a query.
type signupGate struct {
	mu     sync.Mutex
	recent []time.Time
}

func newSignupGate() *signupGate { return &signupGate{} }

// allow reports whether another account may be created now, given the limits,
// and how long to wait if not. Zero for a limit means it is not enforced.
func (g *signupGate) allow(perMinute, perHour int) (bool, time.Duration) {
	if perMinute <= 0 && perHour <= 0 {
		return true, 0
	}

	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()

	// One pass, dropping anything past the longer window and counting the
	// shorter one on the way. The slice is bounded by the hourly limit, so
	// this stays small even under attack.
	kept := g.recent[:0]
	inMinute := 0
	for _, at := range g.recent {
		if now.Sub(at) >= time.Hour {
			continue
		}
		kept = append(kept, at)
		if now.Sub(at) < time.Minute {
			inMinute++
		}
	}
	g.recent = kept

	if perMinute > 0 && inMinute >= perMinute {
		// The oldest entry inside the minute is the one that has to age out.
		oldest := g.recent[len(g.recent)-inMinute]
		return false, time.Minute - now.Sub(oldest)
	}
	if perHour > 0 && len(g.recent) >= perHour {
		return false, time.Hour - now.Sub(g.recent[0])
	}
	return true, 0
}

// record marks an account as created. Called only on success, so a rejected
// attempt does not spend anyone else's allowance.
func (g *signupGate) record() {
	g.mu.Lock()
	g.recent = append(g.recent, time.Now())
	g.mu.Unlock()
}

// checkSignupRate refuses a sign-up that would pass the per-minute or per-hour
// limit. It is counted from the account_creations rows, under the instance lock,
// so the count and the account it allows cannot interleave with another
// sign-up, in this process or another one sharing the database. Zero for a limit
// means it is not enforced.
func (s *Service) checkSignupRate(ctx context.Context, tx *database.Tx) error {
	perMinute := s.settings.Int(settings.SignupsPerMinute, 0)
	perHour := s.settings.Int(settings.SignupsPerHour, 0)
	if perMinute <= 0 && perHour <= 0 {
		return nil
	}
	if err := settings.Lock(ctx, tx); err != nil {
		return err
	}

	now := time.Now()
	var inMinute int
	var oldestMinute int64
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(MIN(created_at), 0) FROM account_creations WHERE created_at > ?`,
		now.Add(-time.Minute).UnixMilli()).Scan(&inMinute, &oldestMinute); err != nil {
		return fmt.Errorf("auth: count sign-ups in the minute: %w", err)
	}
	if perMinute > 0 && inMinute >= perMinute {
		return &SignupThrottleError{RetryAfter: time.Until(time.UnixMilli(oldestMinute).Add(time.Minute))}
	}

	var inHour int
	var oldestHour int64
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(MIN(created_at), 0) FROM account_creations WHERE created_at > ?`,
		now.Add(-time.Hour).UnixMilli()).Scan(&inHour, &oldestHour); err != nil {
		return fmt.Errorf("auth: count sign-ups in the hour: %w", err)
	}
	if perHour > 0 && inHour >= perHour {
		return &SignupThrottleError{RetryAfter: time.Until(time.UnixMilli(oldestHour).Add(time.Hour))}
	}
	return nil
}

// recordAccountCreation writes the sign-up the limit counts, inside the same
// transaction as the account, and drops the rows that have left the hour.
func recordAccountCreation(ctx context.Context, tx *database.Tx) error {
	now := time.Now()
	if _, err := tx.Exec(ctx,
		`DELETE FROM account_creations WHERE created_at <= ?`, now.Add(-time.Hour).UnixMilli()); err != nil {
		return fmt.Errorf("auth: prune sign-up record: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO account_creations (created_at) VALUES (?)`, now.UnixMilli()); err != nil {
		return fmt.Errorf("auth: record sign-up: %w", err)
	}
	return nil
}

// checkEmail applies the two email settings: whether one is needed at all,
// and which domains are acceptable.
func checkEmail(set *settings.Service, email string) error {
	address := strings.TrimSpace(email)

	if address == "" {
		if set.Bool(settings.RequireEmail) {
			return ErrEmailRequired
		}
		return nil
	}

	allowed := parseDomains(set.Get(settings.EmailDomains))
	if len(allowed) == 0 {
		return nil
	}

	at := strings.LastIndex(address, "@")
	if at < 0 {
		return &EmailDomainError{Allowed: allowed}
	}
	domain := strings.ToLower(address[at+1:])
	for _, candidate := range allowed {
		if domain == candidate {
			return nil
		}
	}
	return &EmailDomainError{Allowed: allowed}
}

// parseDomains reads the setting's comma- or newline-separated list. A stray
// "@" or a pasted "https://gmail.com" is tolerated rather than silently
// rejecting every address.
func ParseDomains(raw string) []string { return parseDomains(raw) }

func parseDomains(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == ';'
	})

	out := make([]string, 0, len(fields))
	for _, field := range fields {
		domain := strings.ToLower(strings.TrimSpace(field))
		domain = strings.TrimPrefix(domain, "@")
		domain = strings.TrimPrefix(domain, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		domain = strings.Trim(domain, ".")
		if domain != "" {
			out = append(out, domain)
		}
	}
	return out
}
