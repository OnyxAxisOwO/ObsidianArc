package pow

import (
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

const (
	// Difficulty looks at the last ten minutes, kept as one counter per
	// minute: a list of timestamps grows with the traffic it is measuring and
	// has to be walked on every request, on an endpoint anyone can call.
	windowSlots = 10

	// Challenges one address may ask for per minute.
	challengesPerMinute = 30

	// Addresses remembered by each map. Past it a new address is refused
	// (the challenge limit) or simply not tracked (difficulty), because the
	// alternative is memory that grows with however many addresses a caller
	// can produce — and a /64 is only one of the ways to produce them.
	maxTrackedAddresses = 50_000

	// How often every map is walked for dead keys, and how soon a full map
	// may be walked again so that a flood of new keys cannot make each
	// request pay for a scan.
	trackerSweepInterval = time.Minute
	fullSweepInterval    = time.Second
)

// minuteCounts counts events over the last windowSlots minutes in fixed
// space. A slot is reused once its minute is more than the window old.
type minuteCounts struct {
	minute [windowSlots]int64
	count  [windowSlots]int
}

func minuteOf(t time.Time) int64 { return t.Unix() / 60 }

func (c *minuteCounts) add(now time.Time) {
	m := minuteOf(now)
	slot := ((m % windowSlots) + windowSlots) % windowSlots
	if c.minute[slot] != m {
		c.minute[slot] = m
		c.count[slot] = 0
	}
	c.count[slot]++
}

// total is the count over the current minute and the nine before it. A
// timestamp ahead of now is counted, as it always was: callers pass their
// own clock and a slightly future event is still recent.
func (c *minuteCounts) total(now time.Time) int {
	m := minuteOf(now)
	sum := 0
	for i := range c.minute {
		if c.minute[i] > m-windowSlots {
			sum += c.count[i]
		}
	}
	return sum
}

// Tracker tracks request frequencies for rate limiting and dynamic difficulty scaling.
//
// Keys are httpx.RateKey of the address, so every address in an IPv6 /64
// counts as one caller.
type Tracker struct {
	mu          sync.Mutex
	ipRequests  map[string]*minuteCounts
	site        minuteCounts
	ipChallenge map[string][]time.Time // at most challengesPerMinute per address
	lastSwept   time.Time
}

func NewTracker() *Tracker {
	return &Tracker{
		ipRequests:  make(map[string]*minuteCounts),
		ipChallenge: make(map[string][]time.Time),
	}
}

// sweepLocked drops every key with nothing left inside its window. Without it
// a key outlived its last request for good, one per address ever seen.
func (t *Tracker) sweepLocked(now time.Time) {
	t.lastSwept = now
	cutoff := now.Add(-time.Minute)
	for key, list := range t.ipChallenge {
		if len(list) == 0 || !list[len(list)-1].After(cutoff) {
			delete(t.ipChallenge, key)
		}
	}
	for key, counts := range t.ipRequests {
		if counts.total(now) == 0 {
			delete(t.ipRequests, key)
		}
	}
}

// sweepIfFullLocked makes room when a map has hit its ceiling, at most once
// a second so a flood of new keys cannot make every request pay for a scan.
// Callers re-read the size afterwards.
func (t *Tracker) sweepIfFullLocked(size int, now time.Time) {
	if size >= maxTrackedAddresses && now.Sub(t.lastSwept) >= fullSweepInterval {
		t.sweepLocked(now)
	}
}

// CheckChallengeRate limits the generation of challenges per IP (max 30/min).
func (t *Tracker) CheckChallengeRate(ip string, now time.Time) error {
	key := httpx.RateKey(ip)

	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(t.lastSwept) >= trackerSweepInterval {
		t.sweepLocked(now)
	}

	cutoff := now.Add(-time.Minute)
	list, known := t.ipChallenge[key]
	valid := list[:0]
	for _, ts := range list {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	if len(valid) >= challengesPerMinute {
		t.ipChallenge[key] = valid
		return ErrRateLimited
	}
	if !known {
		t.sweepIfFullLocked(len(t.ipChallenge), now)
		if len(t.ipChallenge) >= maxTrackedAddresses {
			// Refused rather than let through: an address that cannot be
			// counted cannot be limited, and this is the endpoint a flood of
			// addresses would aim at.
			return ErrRateLimited
		}
	}
	t.ipChallenge[key] = append(valid, now)
	return nil
}

// RecordAttempt records a registration or challenge attempt for dynamic difficulty.
func (t *Tracker) RecordAttempt(ip string, now time.Time) {
	key := httpx.RateKey(ip)

	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(t.lastSwept) >= trackerSweepInterval {
		t.sweepLocked(now)
	}

	t.site.add(now)

	counts := t.ipRequests[key]
	if counts == nil {
		t.sweepIfFullLocked(len(t.ipRequests), now)
		if len(t.ipRequests) >= maxTrackedAddresses {
			// Still counted site-wide, which is what raises the difficulty
			// for everyone when many addresses show up at once.
			return
		}
		counts = &minuteCounts{}
		t.ipRequests[key] = counts
	}
	counts.add(now)
}

// DetermineMaxNumber picks base or elevated difficulty based on 10-minute activity.
func (t *Tracker) DetermineMaxNumber(ip string, baseMax, elevatedMax int64, threshold int, now time.Time) int64 {
	if threshold <= 0 {
		return baseMax
	}
	key := httpx.RateKey(ip)

	t.mu.Lock()
	defer t.mu.Unlock()

	ipCount := 0
	if counts := t.ipRequests[key]; counts != nil {
		ipCount = counts.total(now)
	}
	if ipCount >= threshold || t.site.total(now) >= threshold {
		return elevatedMax
	}
	return baseMax
}
