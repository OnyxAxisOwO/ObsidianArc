package auth

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
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

// signupGate counts accounts created in the last minute and the last hour.
//
// Instance-wide rather than per address: the abuse this is for is a script
// making accounts, and a script that is worth throttling has more than one
// address. In memory, like the login limiter, for the same reasons — a
// counter table would be a write per attempt, and a restart forgiving the
// window is an acceptable trade for one binary.
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

// checkQQ applies the QQ setting: whether one is needed at all,
// and ensures it has a valid format when provided.
func checkQQ(set *settings.Service, qq string) error {
	number := strings.TrimSpace(qq)
	req := set.Get(settings.QQRequirement)

	if number == "" {
		if req == settings.QQRequired {
			return user.ErrQQRequired
		}
		return nil
	}

	return user.ValidateQQ(number)
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
