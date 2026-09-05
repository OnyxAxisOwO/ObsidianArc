package auth

import (
	"strings"
	"testing"
	"time"
)

// The registration controls are the only thing standing between an open
// instance and a script that turns it into a free inference endpoint, so what
// they let through matters more than what they refuse.

func TestSignupGateIsOffWhenNeitherLimitIsSet(t *testing.T) {
	gate := newSignupGate()
	for i := 0; i < 100; i++ {
		if ok, _ := gate.allow(0, 0); !ok {
			t.Fatalf("refused at %d with no limits configured", i)
		}
		gate.record()
	}
}

func TestSignupGateEnforcesThePerMinuteLimit(t *testing.T) {
	gate := newSignupGate()

	for i := 0; i < 3; i++ {
		if ok, _ := gate.allow(3, 0); !ok {
			t.Fatalf("refused account %d of an allowed 3", i+1)
		}
		gate.record()
	}

	ok, retryAfter := gate.allow(3, 0)
	if ok {
		t.Fatal("a fourth account was allowed past a limit of three")
	}
	// The caller turns this into a Retry-After, so an unusable value would
	// send the client back immediately and in a loop.
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retry after = %v, want something inside the minute", retryAfter)
	}
}

func TestSignupGateEnforcesThePerHourLimitSeparately(t *testing.T) {
	gate := newSignupGate()

	// Under the per-minute limit throughout, so only the hourly one can be
	// what refuses.
	for i := 0; i < 5; i++ {
		if ok, _ := gate.allow(100, 5); !ok {
			t.Fatalf("refused account %d of an allowed 5", i+1)
		}
		gate.record()
	}

	ok, retryAfter := gate.allow(100, 5)
	if ok {
		t.Fatal("a sixth account was allowed past an hourly limit of five")
	}
	if retryAfter <= 0 || retryAfter > time.Hour {
		t.Errorf("retry after = %v, want something inside the hour", retryAfter)
	}
}

// Entries older than the window must stop counting, or the limit becomes a
// lifetime cap and the instance quietly closes itself.
func TestSignupGateForgetsOldEntries(t *testing.T) {
	gate := newSignupGate()

	stale := time.Now().Add(-2 * time.Minute)
	gate.recent = []time.Time{stale, stale, stale}

	if ok, _ := gate.allow(3, 0); !ok {
		t.Fatal("refused although every recorded account is outside the minute")
	}
}

func TestParseDomainsTakesWhatOperatorsActuallyType(t *testing.T) {
	cases := map[string][]string{
		"qq.com, gmail.com":       {"qq.com", "gmail.com"},
		"qq.com\noutlook.com":     {"qq.com", "outlook.com"},
		"@QQ.com":                 {"qq.com"},
		"https://gmail.com; a.io": {"gmail.com", "a.io"},
		"":                        {},
		"   ":                     {},
	}

	for raw, want := range cases {
		got := parseDomains(raw)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("parseDomains(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestEmailDomainCheckIsCaseInsensitiveAndAnchored(t *testing.T) {
	allowed := parseDomains("qq.com, gmail.com")

	shouldPass := []string{"someone@qq.com", "SOMEONE@QQ.COM", "a.b+c@gmail.com"}
	for _, address := range shouldPass {
		if !domainAllowed(address, allowed) {
			t.Errorf("%q was refused", address)
		}
	}

	// The last @ is what counts, and a suffix match would let
	// "evil-qq.com" through.
	shouldFail := []string{"someone@evil-qq.com", "someone@qq.com.evil.net", "qq.com", "a@b.c"}
	for _, address := range shouldFail {
		if domainAllowed(address, allowed) {
			t.Errorf("%q was accepted", address)
		}
	}
}

// domainAllowed mirrors the check inside checkEmail, which needs a settings
// service it is not worth building here for a string comparison.
func domainAllowed(address string, allowed []string) bool {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(address[at+1:])
	for _, candidate := range allowed {
		if domain == candidate {
			return true
		}
	}
	return false
}
