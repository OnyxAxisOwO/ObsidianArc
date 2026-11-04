package systembackup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// An automatic run that failed is retried after a short delay, not a full
// interval later. The interval is still measured from the last stored archive,
// so a failure does not move the recovery point.
func TestAFailedAutomaticRunIsRetriedAfterTheRetryDelay(t *testing.T) {
	store := savedS3Store(t)
	ctx := context.Background()
	now := time.Now()

	success := now.Add(-10 * time.Hour)
	token, err := store.claim(ctx, success, true)
	if err != nil || token == "" {
		t.Fatalf("claim the first run: %v (token %q)", err, token)
	}
	if err := store.finish(ctx, token, "success", "", success, success.UnixMilli()); err != nil {
		t.Fatalf("finish the first run: %v", err)
	}

	failed := now.Add(-10 * time.Minute)
	token, err = store.claim(ctx, failed, true)
	if err != nil || token == "" {
		t.Fatalf("claim the failing run: %v (token %q)", err, token)
	}
	if err := store.finish(ctx, token, "error", "upstream refused the upload", failed, 0); err != nil {
		t.Fatalf("finish the failing run: %v", err)
	}

	status, err := store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := failed.Add(retryAfterFailure).UnixMilli(); status.NextRunAt != want {
		t.Fatalf("next run after a failure = %d, want the retry delay %d", status.NextRunAt, want)
	}

	if token, err := store.claim(ctx, now, false); err != nil || token != "" {
		t.Fatalf("a failure ten minutes ago was retried at once: token %q, err %v", token, err)
	}
	token, err = store.claim(ctx, failed.Add(retryAfterFailure+time.Minute), false)
	if err != nil || token == "" {
		t.Fatalf("the failed run was not retried after the retry delay: token %q, err %v", token, err)
	}
}

// A failure message is cut to a bounded length, and a byte cut can split a
// multi-byte character. SQLite stores the broken bytes without complaint, so
// the check is on the stored value itself; PostgreSQL would refuse the write.
func TestAFailureMessageIsStoredAsValidUTF8WhenItIsCut(t *testing.T) {
	store := savedS3Store(t)
	ctx := context.Background()
	now := time.Now()
	token, err := store.claim(ctx, now, true)
	if err != nil || token == "" {
		t.Fatalf("claim: %v (token %q)", err, token)
	}

	// The three-byte character starts at byte 599, so a 600-byte cut splits it.
	failure := strings.Repeat("a", 599) + "界" + strings.Repeat("b", 200)
	if err := store.finish(ctx, token, "error", failure, now, 0); err != nil {
		t.Fatalf("finish: %v", err)
	}

	var stored string
	if err := store.db.QueryRow(ctx, `SELECT last_error FROM system_backups WHERE id = ?`, singletonID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(stored) {
		t.Fatalf("stored failure is not valid UTF-8: %q", stored)
	}
	if n := len([]rune(stored)); n > 600 {
		t.Fatalf("stored failure has %d runes, want at most 600", n)
	}
	status, err := store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Running || status.LastStatus != "error" {
		t.Fatalf("the lease was not released: status %q running %v", status.LastStatus, status.Running)
	}
}

// A run that keeps failing waits longer after each failure, up to the interval,
// so a persistent outage costs one attempt an interval rather than one every
// retry delay. A success starts the count again.
func TestRepeatedFailuresBackOffUpToTheInterval(t *testing.T) {
	store := savedS3Store(t)
	ctx := context.Background()
	anchor := time.Now()
	interval := time.Duration(DefaultIntervalHours) * time.Hour

	var gaps []time.Duration
	for i := 0; i < 8; i++ {
		token, err := store.claim(ctx, anchor, true)
		if err != nil || token == "" {
			t.Fatalf("claim failure %d: %v (token %q)", i+1, err, token)
		}
		if err := store.finish(ctx, token, "error", "upstream refused the upload", anchor, 0); err != nil {
			t.Fatalf("finish failure %d: %v", i+1, err)
		}
		status, err := store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		gaps = append(gaps, time.Duration(status.NextRunAt-anchor.UnixMilli())*time.Millisecond)
	}

	if gaps[0] != retryAfterFailure {
		t.Fatalf("first failure waits %v, want the retry delay %v", gaps[0], retryAfterFailure)
	}
	if gaps[2] <= gaps[0] {
		t.Fatalf("third failure waits %v, no longer than the first (%v)", gaps[2], gaps[0])
	}
	for i, gap := range gaps {
		if gap > interval {
			t.Fatalf("failure %d waits %v, longer than the interval %v", i+1, gap, interval)
		}
	}
	if last := gaps[len(gaps)-1]; last != interval {
		t.Fatalf("a long outage settles at %v, want the interval %v", last, interval)
	}

	failures, err := failureCount(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if failures != 8 {
		t.Fatalf("consecutive failures = %d, want 8", failures)
	}

	token, err := store.claim(ctx, anchor, true)
	if err != nil || token == "" {
		t.Fatalf("claim the recovering run: %v (token %q)", err, token)
	}
	if err := store.finish(ctx, token, "success", "", anchor, anchor.UnixMilli()); err != nil {
		t.Fatalf("finish the recovering run: %v", err)
	}
	if failures, err = failureCount(ctx, store.db); err != nil {
		t.Fatal(err)
	}
	if failures != 0 {
		t.Fatalf("consecutive failures after a success = %d, want 0", failures)
	}
}

// Claims and finishes racing from several goroutines, as two instances would,
// all serialise on the scheduler's own row lock: every run that was claimed and
// then failed is counted exactly once, with none lost to a stale read.
func TestConcurrentRunsCountEveryFailureOnce(t *testing.T) {
	store := savedS3Store(t)
	ctx := context.Background()
	anchor := time.Now()

	const workers, rounds = 4, 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed int
	)
	errs := make(chan error, workers*rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				token, err := store.claim(ctx, anchor, true)
				if errors.Is(err, ErrAlreadyRunning) {
					continue
				}
				if err != nil {
					errs <- err
					return
				}
				if token == "" {
					continue
				}
				if err := store.finish(ctx, token, "error", "upstream refused the upload", anchor, 0); err != nil {
					errs <- err
					return
				}
				mu.Lock()
				claimed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if claimed == 0 {
		t.Fatal("no run was ever claimed")
	}
	failures, err := failureCount(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if failures != claimed {
		t.Fatalf("consecutive failures = %d, want one per claimed run (%d)", failures, claimed)
	}
}

func TestRetentionFallsBackFromDaysToTheDefault(t *testing.T) {
	cases := []struct {
		cfg  Config
		want int
	}{
		{Config{RetentionHours: 5, RetentionDays: 9}, 5},
		{Config{RetentionDays: 2}, 48},
		{Config{}, DefaultRetentionHours},
	}
	for _, c := range cases {
		if got := effectiveRetentionHours(c.cfg); got != c.want {
			t.Fatalf("effectiveRetentionHours(%+v) = %d, want %d", c.cfg, got, c.want)
		}
	}
	for hours, days := range map[int]int{1: 1, 24: 1, 25: 2, 168: 7} {
		if got := retentionDays(hours); got != days {
			t.Fatalf("retentionDays(%d) = %d, want %d", hours, got, days)
		}
	}
}
