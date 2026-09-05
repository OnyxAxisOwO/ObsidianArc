package conversation

import (
	"context"
	"testing"
	"time"
)

func at(day, hour, minute int) time.Time {
	return time.Date(2026, 9, day, hour, minute, 0, 0, time.Local)
}

func TestParseDailyTime(t *testing.T) {
	good := map[string][2]int{
		"03:00": {3, 0},
		"0:5":   {0, 5},
		"23:59": {23, 59},
		" 7:30": {7, 30},
	}
	for value, want := range good {
		hour, minute, ok := ParseDailyTime(value)
		if !ok || hour != want[0] || minute != want[1] {
			t.Errorf("ParseDailyTime(%q) = %d, %d, %v", value, hour, minute, ok)
		}
	}

	for _, bad := range []string{"", "   ", "3", "24:00", "12:60", "-1:00", "noon", "3:00:00", "a:b"} {
		if _, _, ok := ParseDailyTime(bad); ok {
			t.Errorf("ParseDailyTime(%q) was accepted", bad)
		}
	}
}

// An unconfigured schedule must never destroy anything, whatever else is true.
func TestNoScheduleNeverPurges(t *testing.T) {
	for _, value := range []string{"", "   ", "nonsense"} {
		if got := DecidePurge(value, at(6, 12, 0), 0); got != DecisionSkip {
			t.Errorf("DecidePurge(%q) = %v, want skip", value, got)
		}
	}
}

// Setting a 03:00 cleanup at three in the afternoon must not wipe everything
// on the spot. The first pass records the time and purges nothing.
func TestFirstPassSeedsRatherThanPurges(t *testing.T) {
	if got := DecidePurge("03:00", at(6, 15, 0), 0); got != DecisionSeed {
		t.Errorf("with no recorded run: %v, want seed", got)
	}
}

func TestPurgeRunsOnceAfterTheScheduledMoment(t *testing.T) {
	yesterday := at(5, 3, 0).UnixMilli()

	// Before today's moment: not yet.
	if got := DecidePurge("03:00", at(6, 2, 59), yesterday); got != DecisionSkip {
		t.Errorf("two minutes early: %v, want skip", got)
	}
	// After it, with the last run before it: owed.
	if got := DecidePurge("03:00", at(6, 3, 1), yesterday); got != DecisionRun {
		t.Errorf("one minute late: %v, want run", got)
	}
	// Having run, the rest of the day is quiet.
	today := at(6, 3, 1).UnixMilli()
	for _, now := range []time.Time{at(6, 3, 11), at(6, 12, 0), at(6, 23, 59)} {
		if got := DecidePurge("03:00", now, today); got != DecisionSkip {
			t.Errorf("at %v after running: %v, want skip", now, got)
		}
	}
	// And it comes back round tomorrow.
	if got := DecidePurge("03:00", at(7, 3, 5), today); got != DecisionRun {
		t.Errorf("the next day: %v, want run", got)
	}
}

// The janitor ticks every ten minutes, so the scheduled moment is rarely a
// tick. Missing it entirely would mean the purge never runs.
func TestPurgeSurvivesTicksThatMissTheMoment(t *testing.T) {
	lastRun := at(5, 3, 0).UnixMilli()
	runs := 0
	for _, minute := range []int{40, 50, 0, 10, 20} { // 02:40 … 03:20 in ten-minute steps
		hour := 2
		if minute < 40 {
			hour = 3
		}
		if DecidePurge("03:00", at(6, hour, minute), lastRun) == DecisionRun {
			runs++
			lastRun = at(6, hour, minute).UnixMilli()
		}
	}
	if runs != 1 {
		t.Errorf("the purge ran %d times across the window, want exactly 1", runs)
	}
}

// A server that was down all day should find the morning's purge outstanding
// rather than skipping a day.
func TestPurgeOwedAfterAnOutageIsStillRun(t *testing.T) {
	lastRun := at(4, 3, 0).UnixMilli()
	if got := DecidePurge("03:00", at(6, 23, 30), lastRun); got != DecisionRun {
		t.Errorf("after two days down: %v, want run", got)
	}
}

// --- what a sweep actually does ---------------------------------------------

func TestSweepDropsBytesByAgeAndKeepsTheRecord(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	old := f.attach(t, daysAgo(30))
	fresh := f.attach(t, daysAgo(1))

	result, err := f.store.Sweep(ctx, Retention{AfterDays: 7}, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Aged != 1 {
		t.Fatalf("aged out %d, want 1", result.Aged)
	}

	if _, _, err := f.store.Blob(ctx, f.userID, old); err != ErrAttachmentDiscarded {
		t.Errorf("the old image is still readable: %v", err)
	}
	if _, _, err := f.store.Blob(ctx, f.userID, fresh); err != nil {
		t.Errorf("the recent image was dropped too: %v", err)
	}

	// The rows survive, so a transcript can still show that a picture was
	// there. Only one of them still holds bytes.
	count, bytes, err := f.store.Held(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || bytes == 0 {
		t.Errorf("held = %d rows, %d bytes; want just the recent one", count, bytes)
	}
}

func TestDailyPurgeTakesEverythingRegardlessOfAge(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	f.attach(t, daysAgo(30))
	f.attach(t, time.Now().Add(-time.Minute).UnixMilli())

	// Due: the schedule is midnight and something ran the day before.
	lastRun := time.Now().Add(-36 * time.Hour).UnixMilli()
	result, err := f.store.Sweep(ctx, Retention{DailyAt: "00:00"}, lastRun)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RanDaily || result.Purged != 2 {
		t.Fatalf("result = %+v, want both purged", result)
	}

	count, _, err := f.store.Held(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("%d attachments still hold bytes after a full purge", count)
	}
}

// An upload still sitting in someone's composer has not been sent. Wiping it
// would break a message being written, so only the orphan window governs it.
func TestPurgeLeavesUnsentUploadsAlone(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)

	pending, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatal(err)
	}

	lastRun := time.Now().Add(-36 * time.Hour).UnixMilli()
	if _, err := f.store.Sweep(ctx, Retention{DailyAt: "00:00", OrphanTTL: time.Hour}, lastRun); err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.store.Blob(ctx, f.userID, pending.ID); err != nil {
		t.Errorf("an upload nobody had sent was purged: %v", err)
	}
}

func TestSweepWithNoPolicyDoesNothing(t *testing.T) {
	ctx := context.Background()
	f := newAttachmentFixture(t)
	id := f.attach(t, daysAgo(400))

	result, err := f.store.Sweep(ctx, Retention{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result != (SweepResult{}) {
		t.Errorf("result = %+v, want nothing done", result)
	}
	if _, _, err := f.store.Blob(ctx, f.userID, id); err != nil {
		t.Errorf("an image was dropped with no policy set: %v", err)
	}
}

func daysAgo(days int) int64 {
	return time.Now().AddDate(0, 0, -days).UnixMilli()
}

// --- fixture -----------------------------------------------------------------

type retentionFixture struct {
	store  *Store
	userID string
}

func newAttachmentFixture(t *testing.T) *retentionFixture {
	t.Helper()
	store, _, account := attachmentFixture(t)
	return &retentionFixture{store: store, userID: account.ID}
}

// attach uploads an image, hangs it off a real message, and backdates the row
// so an age policy has something to act on.
func (f *retentionFixture) attach(t *testing.T, createdAt int64) string {
	t.Helper()
	ctx := context.Background()

	uploaded, err := f.store.Upload(ctx, UploadInput{
		UserID: f.userID, Mime: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	thread, err := f.store.Create(ctx, nil, f.userID, "thread", "")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := f.store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID,
		UserID:         f.userID,
		Role:           RoleUser,
		Content:        "look at this",
		AttachmentIDs:  []string{uploaded.ID},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if _, err := f.store.db.Exec(ctx,
		`UPDATE attachments SET created_at = ? WHERE id = ?`, createdAt, uploaded.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	return uploaded.ID
}
