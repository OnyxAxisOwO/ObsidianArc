package admin

import (
	"testing"
	"time"
)

// A counter is not a rate. The process reports CPU time since it started, so
// the first request has nothing to divide by — and answering it with a week
// of uptime under an hour of work would print "0.6%" on an instance that is
// pinned right now.
func TestTheFirstCPUSampleHasNoAnswer(t *testing.T) {
	var sampler cpuSampler
	start := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	if _, _, ok := sampler.rate(start, 90*time.Minute); ok {
		t.Fatal("the first reading reported a rate")
	}

	// Half a second of CPU across one second of wall clock is half a core.
	percent, window, ok := sampler.rate(start.Add(time.Second), 90*time.Minute+500*time.Millisecond)
	if !ok {
		t.Fatal("the second reading reported nothing")
	}
	if percent < 49.9 || percent > 50.1 {
		t.Errorf("percent = %v, want 50", percent)
	}
	if window != 1 {
		t.Errorf("window = %v, want 1 second", window)
	}
}

// Each answer covers the window since the last one, not since the process
// started: a busy minute must not be diluted by an idle hour before it.
func TestEachSampleAnswersForItsOwnWindow(t *testing.T) {
	var sampler cpuSampler
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	sampler.rate(now, 0)
	// An idle minute.
	sampler.rate(now.Add(time.Minute), 0)
	// Then a second in which two cores were busy throughout.
	percent, window, ok := sampler.rate(now.Add(time.Minute+time.Second), 2*time.Second)
	if !ok {
		t.Fatal("no rate")
	}
	if percent < 199 || percent > 201 {
		t.Errorf("percent = %v, want about 200 — two cores of one", percent)
	}
	if window != 1 {
		t.Errorf("window = %v, want the last second only", window)
	}
}

// Two requests inside the same instant, or a clock that stepped backwards.
// Dividing by that window is an infinity on an operator's screen.
func TestAnEmptyWindowIsNotARate(t *testing.T) {
	var sampler cpuSampler
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	sampler.rate(now, time.Second)
	if _, _, ok := sampler.rate(now, 2*time.Second); ok {
		t.Error("two readings in the same instant produced a rate")
	}
	if _, _, ok := sampler.rate(now.Add(-time.Minute), 3*time.Second); ok {
		t.Error("a clock that went backwards produced a rate")
	}
}

// The platform half. Whatever it answers must be usable as a counter: never
// negative, and never going backwards between two reads.
func TestProcessCPUIsAMonotonicCounter(t *testing.T) {
	first, ok := processCPU()
	if !ok {
		t.Skip("no process CPU on this platform")
	}
	if first < 0 {
		t.Fatalf("negative CPU time: %v", first)
	}
	// Something to actually spend CPU on, so the second read has somewhere to
	// go and the test is not just reading the same number twice.
	sink := 0
	for i := range 4_000_000 {
		sink += i % 7
	}
	second, _ := processCPU()
	if second < first {
		t.Errorf("CPU time went backwards: %v then %v (sink %d)", first, second, sink)
	}
}
