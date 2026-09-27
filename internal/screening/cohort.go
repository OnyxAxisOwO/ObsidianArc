package screening

import (
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// RecentSignup is the small part of a stored account that can show whether
// the current registration belongs to a repeated batch. No other person's
// full address, contact number, IP or browser header is sent to the model.
type RecentSignup struct {
	Username             string
	Email                string
	Status               user.Status
	APIRestricted        bool
	APIRestrictedUntil   int64
	APIRestrictionSource string
	CreatedAt            int64
}

type signupCohort struct {
	domain, usernamePattern, localPattern      string
	sameDomain, matching, disabled, restricted int
	firstAt, lastAt                            int64
}

// numericPattern keeps a chosen prefix and the number of digits. A number
// alone is far too common to refuse; it becomes useful only when two field
// patterns and a mail domain recur together among recent accounts.
func numericPattern(value string) (string, bool) {
	var out strings.Builder
	longest, run := 0, 0
	for _, char := range strings.ToLower(value) {
		if char >= '0' && char <= '9' {
			out.WriteByte('0')
			run++
			if run > longest {
				longest = run
			}
		} else {
			out.WriteRune(char)
			run = 0
		}
	}
	return out.String(), longest >= 4
}

func emailParts(email string) (local, domain string) {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return "", ""
	}
	return email[:at], email[at+1:]
}

func cohortFor(facts Facts) signupCohort {
	now := time.Now().UnixMilli()
	local, domain := emailParts(facts.Email)
	userPattern, userNumeric := numericPattern(facts.Username)
	localPattern, localNumeric := numericPattern(local)
	// A QQ number used as both handle and qq.com local part is a common
	// personal choice, even when several people registered together.
	if domain == "qq.com" && strings.EqualFold(facts.Username, local) {
		userNumeric, localNumeric = false, false
	}
	cohort := signupCohort{domain: domain, usernamePattern: userPattern, localPattern: localPattern}
	if domain == "" {
		return cohort
	}
	for _, item := range facts.Recent {
		otherLocal, otherDomain := emailParts(item.Email)
		if otherDomain != domain {
			continue
		}
		cohort.sameDomain++
		if !userNumeric || !localNumeric {
			continue
		}
		otherUserPattern, _ := numericPattern(item.Username)
		otherLocalPattern, _ := numericPattern(otherLocal)
		if otherUserPattern != userPattern || otherLocalPattern != localPattern {
			continue
		}
		cohort.matching++
		if item.CreatedAt > 0 {
			if cohort.firstAt == 0 || item.CreatedAt < cohort.firstAt {
				cohort.firstAt = item.CreatedAt
			}
			if item.CreatedAt > cohort.lastAt {
				cohort.lastAt = item.CreatedAt
			}
		}
		if item.Status == user.StatusDisabled {
			cohort.disabled++
		}
		if item.APIRestricted && item.APIRestrictionSource == "signup_review" &&
			(item.APIRestrictedUntil == 0 || item.APIRestrictedUntil > now) {
			cohort.restricted++
		}
	}
	return cohort
}

func (c signupCohort) description() string {
	if c.domain == "" {
		return "No comparable email domain."
	}
	span := ""
	if c.firstAt > 0 && c.lastAt > c.firstAt {
		span = fmt.Sprintf(" across %d minutes", (c.lastAt-c.firstAt)/60000)
	}
	return fmt.Sprintf("Recent same-domain accounts: %d; both numeric templates match: %d%s; matching accounts disabled: %d, active signup-review API restrictions: %d. Templates: %s / %s@%s.",
		c.sameDomain, c.matching, span, c.disabled, c.restricted, c.usernamePattern, c.localPattern, c.domain)
}

// Refusal requires a dense batch plus two distinct past interventions. A
// shared domain, generated-looking name, or a few disabled peers can each
// arise from ordinary use and should remain for the model to weigh.
func (c signupCohort) confirmed() bool {
	return c.matching >= 5 && c.disabled >= 4 && c.restricted >= 3
}

func (c signupCohort) protect(verdict Verdict) Verdict {
	if !c.confirmed() {
		return verdict
	}
	return Verdict{Decision: DecisionRefuse,
		Reason: fmt.Sprintf("Matches %d recent registrations with the same domain and two account templates; %d are disabled and %d have API restrictions.", c.matching, c.disabled, c.restricted)}
}

// A model can invent browser facts or overread a single odd handle. Holding
// API access is reversible; refusing a real person's registration is not.
func (c signupCohort) protectModel(verdict Verdict) Verdict {
	if c.confirmed() {
		return c.protect(verdict)
	}
	if verdict.Decision == DecisionRefuse {
		return Verdict{Decision: DecisionRestrict,
			Reason: "AI refusal lacked corroborating registration evidence; API access held for review."}
	}
	return verdict
}
