package pow

import (
	"fmt"
	"testing"
	"time"
)

func addr(i int) string { return fmt.Sprintf("10.%d.%d.%d", i>>16&0xff, i>>8&0xff, i&0xff) }

// Every address that ever asked used to leave a key behind for good. The
// endpoint is anonymous, so that was memory anyone could spend.
func TestTrackerForgetsAddressesThatWentQuiet(t *testing.T) {
	tracker := NewTracker()
	start := time.Now()

	for i := 0; i < 2000; i++ {
		ip := addr(i)
		if err := tracker.CheckChallengeRate(ip, start); err != nil {
			t.Fatal(err)
		}
		tracker.RecordAttempt(ip, start)
	}
	if got := len(tracker.ipChallenge); got != 2000 {
		t.Fatalf("challenge keys = %d, want 2000", got)
	}

	later := start.Add(trackerSweepInterval + 11*time.Minute)
	if err := tracker.CheckChallengeRate("198.51.100.1", later); err != nil {
		t.Fatal(err)
	}
	tracker.RecordAttempt("198.51.100.1", later)
	if got := len(tracker.ipChallenge); got != 1 {
		t.Errorf("challenge keys after the window = %d, want only the new caller", got)
	}
	if got := len(tracker.ipRequests); got != 1 {
		t.Errorf("request keys after the window = %d, want only the new caller", got)
	}
}

func TestTrackerRefusesNewAddressesPastItsCeiling(t *testing.T) {
	tracker := NewTracker()
	now := time.Now()

	for i := 0; i < maxTrackedAddresses; i++ {
		if err := tracker.CheckChallengeRate(addr(i), now); err != nil {
			t.Fatalf("address %d refused below the ceiling: %v", i, err)
		}
		tracker.RecordAttempt(addr(i), now)
	}

	if err := tracker.CheckChallengeRate("198.51.100.1", now); err != ErrRateLimited {
		t.Errorf("a new address past the ceiling got %v, want ErrRateLimited", err)
	}
	if len(tracker.ipChallenge) > maxTrackedAddresses {
		t.Errorf("challenge map grew to %d", len(tracker.ipChallenge))
	}
	// Known addresses are still served.
	if err := tracker.CheckChallengeRate(addr(7), now); err != nil {
		t.Errorf("a tracked address was refused: %v", err)
	}

	// Not tracked per address, but still counted for the whole site.
	tracker.RecordAttempt("198.51.100.2", now)
	if len(tracker.ipRequests) > maxTrackedAddresses {
		t.Errorf("request map grew to %d", len(tracker.ipRequests))
	}
	if got := tracker.site.total(now); got != maxTrackedAddresses+1 {
		t.Errorf("site count = %d, want %d", got, maxTrackedAddresses+1)
	}

	// Once the window has passed the room comes back.
	later := now.Add(3 * time.Minute)
	if err := tracker.CheckChallengeRate("198.51.100.1", later); err != nil {
		t.Errorf("a full map never made room: %v", err)
	}
}

func TestTrackerCountsAnIPv6SubnetAsOneCaller(t *testing.T) {
	tracker := NewTracker()
	now := time.Now()

	for i := 0; i < challengesPerMinute; i++ {
		if err := tracker.CheckChallengeRate(fmt.Sprintf("2001:db8:5:6::%x", i+1), now); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if err := tracker.CheckChallengeRate("2001:db8:5:6:1:2:3:4", now); err != ErrRateLimited {
		t.Fatalf("a fresh address in the same /64 got %v, want ErrRateLimited", err)
	}
	if err := tracker.CheckChallengeRate("2001:db8:5:7::1", now); err != nil {
		t.Fatalf("a different /64 was limited: %v", err)
	}

	// The same folding applies to difficulty.
	for i := 0; i < 5; i++ {
		tracker.RecordAttempt(fmt.Sprintf("2001:db8:9:9::%x", i+1), now)
	}
	if got := len(tracker.ipRequests); got != 1 {
		t.Errorf("five addresses in one /64 made %d keys", got)
	}
}

// The site-wide figure is a fixed number of counters, not a list that is
// walked on every request.
func TestSiteActivityIsCountedPerMinuteAndExpires(t *testing.T) {
	tracker := NewTracker()
	now := time.Now()

	for i := 0; i < 5000; i++ {
		tracker.RecordAttempt(addr(i%100), now)
	}
	if got := tracker.site.total(now); got != 5000 {
		t.Fatalf("site count = %d, want 5000", got)
	}
	if d := tracker.DetermineMaxNumber("203.0.113.9", 10, 99, 100, now); d != 99 {
		t.Errorf("difficulty = %d, want elevated", d)
	}
	if d := tracker.DetermineMaxNumber("203.0.113.9", 10, 99, 100, now.Add(11*time.Minute)); d != 10 {
		t.Errorf("difficulty after the window = %d, want base", d)
	}
	if got := tracker.site.total(now.Add(5 * time.Minute)); got != 5000 {
		t.Errorf("site count five minutes on = %d, want it still inside the window", got)
	}
}
