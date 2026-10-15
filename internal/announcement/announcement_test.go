package announcement

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	store *Store
	users *user.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "announcement.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	return &fixture{store: NewStore(db), users: user.NewStore(db)}
}

func (f *fixture) reader(t *testing.T, username string) user.User {
	t.Helper()
	record, err := f.users.Create(context.Background(), nil, user.CreateInput{
		Username:     username,
		PasswordHash: "not-a-real-hash",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return record
}

func (f *fixture) write(t *testing.T, in Input) Announcement {
	t.Helper()
	record, err := f.store.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create announcement: %v", err)
	}
	return record
}

// A draft is the whole point of the published flag: it must be invisible to
// readers while still being there for whoever is writing it.
func TestDraftsAreInvisibleToReaders(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "reader")

	f.write(t, Input{Title: "Draft", Published: false})
	f.write(t, Input{Title: "Live", Published: true})

	all, err := f.store.ListAll(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("administrator sees %d, want both", len(all))
	}

	mine, err := f.store.ListFor(ctx, reader.ID)
	if err != nil {
		t.Fatalf("list for reader: %v", err)
	}
	if len(mine) != 1 || mine[0].Title != "Live" {
		t.Fatalf("reader sees %d rows (%+v), want only the published one", len(mine), mine)
	}
}

// Read state is per reader: one person dismissing an announcement must not
// clear it for everyone.
func TestReadStateIsPerReader(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first := f.reader(t, "first")
	second := f.reader(t, "second")
	record := f.write(t, Input{Title: "Notice", Published: true})

	if err := f.store.MarkRead(ctx, first.ID, record.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	mine, _ := f.store.ListFor(ctx, first.ID)
	if len(mine) != 1 || !mine[0].Read {
		t.Error("the reader who dismissed it is not marked read")
	}
	theirs, _ := f.store.ListFor(ctx, second.ID)
	if len(theirs) != 1 || theirs[0].Read {
		t.Error("dismissing it for one reader marked it read for another")
	}
}

// Opening the same announcement twice is not an error, and must not lose the
// original read time by inserting a second row.
func TestMarkReadIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	reader := f.reader(t, "reader")
	record := f.write(t, Input{Title: "Notice", Published: true})

	for i := 0; i < 3; i++ {
		if err := f.store.MarkRead(ctx, reader.ID, record.ID); err != nil {
			t.Fatalf("mark read %d: %v", i, err)
		}
	}
}

// The store refuses a draft itself, not only the handler in front of it. A read
// row written for a draft would stop its popup for good once it is published,
// and the published check has to hold the row lock Update takes, or an unpublish
// could land between the check and the write.
func TestMarkReadRefusesADraftAtTheStore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "reader")
	draft := f.write(t, Input{Title: "Draft", DisplayMode: DisplayOnce})

	if err := f.store.MarkRead(ctx, reader.ID, draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mark read on a draft = %v, want ErrNotFound", err)
	}
	if err := f.store.MarkRead(ctx, reader.ID, "no-such-announcement"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mark read on an unknown id = %v, want ErrNotFound", err)
	}

	if _, err := f.store.Update(ctx, draft.ID, Input{Title: "Draft", DisplayMode: DisplayOnce, Published: true}); err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	mine, err := f.store.ListFor(ctx, reader.ID)
	if err != nil {
		t.Fatalf("list for reader: %v", err)
	}
	if len(mine) != 1 || mine[0].Read {
		t.Fatalf("the draft was marked read before it was published: %+v", mine)
	}
}

func TestMarkAllReadCoversEveryPublishedRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	reader := f.reader(t, "reader")
	f.write(t, Input{Title: "One", Published: true})
	f.write(t, Input{Title: "Two", Published: true})
	f.write(t, Input{Title: "Draft", Published: false})

	if err := f.store.MarkAllRead(ctx, reader.ID); err != nil {
		t.Fatalf("mark all read: %v", err)
	}

	mine, _ := f.store.ListFor(ctx, reader.ID)
	for _, record := range mine {
		if !record.Read {
			t.Errorf("%q is still unread", record.Title)
		}
	}
}

func TestPinnedRisesAboveNewer(t *testing.T) {
	f := newFixture(t)

	f.write(t, Input{Title: "Older but pinned", Published: true, Pinned: true})
	f.write(t, Input{Title: "Newer", Published: true})

	all, err := f.store.ListAll(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 || all[0].Title != "Older but pinned" {
		t.Fatalf("order = %v, want the pinned one first", titles(all))
	}
}

// The dismiss delay is a courtesy, not a trap: an operator who types 600 gets
// the ceiling rather than an error, and a negative is treated as none.
func TestDismissDelayIsClamped(t *testing.T) {
	f := newFixture(t)

	long := f.write(t, Input{Title: "Long", Published: true, DismissAfterSeconds: 600})
	if long.DismissAfterSeconds != MaxDismissSeconds {
		t.Errorf("600 became %d, want %d", long.DismissAfterSeconds, MaxDismissSeconds)
	}

	negative := f.write(t, Input{Title: "Negative", Published: true, DismissAfterSeconds: -5})
	if negative.DismissAfterSeconds != 0 {
		t.Errorf("-5 became %d, want 0", negative.DismissAfterSeconds)
	}
}

func TestUnknownDisplayModeIsRefused(t *testing.T) {
	f := newFixture(t)

	_, err := f.store.Create(context.Background(), Input{
		Title:       "Bad",
		DisplayMode: DisplayMode("shout"),
	})
	if err != ErrInvalidMode {
		t.Fatalf("err = %v, want ErrInvalidMode", err)
	}
}

func TestEmptyDisplayModeBecomesOnce(t *testing.T) {
	f := newFixture(t)

	record := f.write(t, Input{Title: "Unset", Published: true})
	if record.DisplayMode != DisplayOnce {
		t.Errorf("display mode = %q, want %q", record.DisplayMode, DisplayOnce)
	}
}

// Publishing reaches everyone; editing an already-published notice, or
// saving a draft, must not ring the bell a second time or a first one.
func TestPublishingNotifiesEveryoneOnceOnTheTransition(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notifyStore := notify.NewStore(f.store.db)
	f.store.Notify = notifyStore
	everyone := user.User{CreatedAt: 0}

	draft, err := f.store.Create(ctx, Input{Title: "Draft", Published: false})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if notices, err := notifyStore.List(ctx, everyone, 10, 0); err != nil || len(notices) != 0 {
		t.Fatalf("notices after a draft = %+v (err %v), want none", notices, err)
	}

	live := f.write(t, Input{Title: "Maintenance window", Published: true})
	notices, err := notifyStore.List(ctx, everyone, 10, 0)
	if err != nil {
		t.Fatalf("list notices: %v", err)
	}
	if len(notices) != 1 || notices[0].Kind != "announcement" || notices[0].Params["title"] != "Maintenance window" {
		t.Fatalf("notices = %+v, want the one publish notice", notices)
	}

	// Editing the wording of something already live is not "something new".
	if _, err := f.store.Update(ctx, live.ID, Input{Title: "Maintenance window (updated)", Published: true}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if notices, err = notifyStore.List(ctx, everyone, 10, 0); err != nil || len(notices) != 1 {
		t.Fatalf("notices after an edit = %+v (err %v), want still just the one", notices, err)
	}

	// Publishing the draft afterwards is its own transition and does ring
	// the bell.
	if _, err := f.store.Update(ctx, draft.ID, Input{Title: "Draft", Published: true}); err != nil {
		t.Fatalf("publish the draft: %v", err)
	}
	if notices, err = notifyStore.List(ctx, everyone, 10, 0); err != nil || len(notices) != 2 {
		t.Fatalf("notices after publishing the draft = %+v (err %v), want two", notices, err)
	}

	// nil Notify — every instance before this feature — must keep working.
	f.store.Notify = nil
	if _, err := f.store.Create(ctx, Input{Title: "Unnotified", Published: true}); err != nil {
		t.Errorf("create with Notify unset: %v", err)
	}
}

// A retracted headline must not keep ringing in every account's bell for the
// rest of the retention window: unpublishing removes the notice the publish
// produced, the same way it would if a reader had never seen it pushed at
// all.
func TestUnpublishingRetractsTheNotice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notifyStore := notify.NewStore(f.store.db)
	f.store.Notify = notifyStore
	everyone := user.User{CreatedAt: 0}

	live := f.write(t, Input{Title: "Maintenance window", Published: true})
	if notices, err := notifyStore.List(ctx, everyone, 10, 0); err != nil || len(notices) != 1 {
		t.Fatalf("notices after publish = %+v (err %v), want one", notices, err)
	}

	if _, err := f.store.Update(ctx, live.ID, Input{Title: live.Title, Published: false}); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if notices, err := notifyStore.List(ctx, everyone, 10, 0); err != nil || len(notices) != 0 {
		t.Fatalf("notices after unpublishing = %+v (err %v), want none", notices, err)
	}

	// Publishing it again is its own transition and rings the bell again.
	if _, err := f.store.Update(ctx, live.ID, Input{Title: live.Title, Published: true}); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if notices, err := notifyStore.List(ctx, everyone, 10, 0); err != nil || len(notices) != 1 {
		t.Fatalf("notices after republishing = %+v (err %v), want one again", notices, err)
	}
}

// Deleting a published announcement outright must retract it the same way
// unpublishing does — there is no editor left afterwards to unpublish it
// from.
func TestDeletingAPublishedAnnouncementRetractsItsNotice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notifyStore := notify.NewStore(f.store.db)
	f.store.Notify = notifyStore
	everyone := user.User{CreatedAt: 0}

	live := f.write(t, Input{Title: "Going away", Published: true})
	unrelated := f.write(t, Input{Title: "Staying", Published: true})
	if notices, err := notifyStore.List(ctx, everyone, 10, 0); err != nil || len(notices) != 2 {
		t.Fatalf("notices after two publishes = %+v (err %v), want two", notices, err)
	}

	if err := f.store.Delete(ctx, live.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	notices, err := notifyStore.List(ctx, everyone, 10, 0)
	if err != nil {
		t.Fatalf("list notices: %v", err)
	}
	if len(notices) != 1 || notices[0].Params["title"] != unrelated.Title {
		t.Fatalf("notices after deleting one = %+v, want only the unrelated one left", notices)
	}
}

// A silent announcement is in the list and marks the bell. A push would also
// raise a toast on every open tab, which is exactly what silent promises not
// to do, so neither publishing nor editing it may push.
func TestSilentAnnouncementPushesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notifyStore := notify.NewStore(f.store.db)
	f.store.Notify = notifyStore
	everyone := user.User{CreatedAt: 0}

	f.write(t, Input{Title: "Quiet, already live", Published: true, DisplayMode: DisplaySilent})
	draft := f.write(t, Input{Title: "Quiet draft", DisplayMode: DisplaySilent})
	if _, err := f.store.Update(ctx, draft.ID, Input{Title: "Quiet draft", DisplayMode: DisplaySilent, Published: true}); err != nil {
		t.Fatalf("publish silent draft: %v", err)
	}

	notices, err := notifyStore.List(ctx, everyone, 10, 0)
	if err != nil || len(notices) != 0 {
		t.Fatalf("notices for silent publishes = %+v (err %v), want none", notices, err)
	}
}

// Two saves publishing the same draft at once. Before the row lock, each could
// read it as unpublished and each push, so one publication rang the bell twice.
func TestConcurrentPublishRingsOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notifyStore := notify.NewStore(f.store.db)
	f.store.Notify = notifyStore
	everyone := user.User{CreatedAt: 0}

	draft := f.write(t, Input{Title: "Race", DisplayMode: DisplayOnce})

	const saves = 8
	start := make(chan struct{})
	failures := make(chan error, saves)
	var workers sync.WaitGroup
	for range saves {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if _, err := f.store.Update(ctx, draft.ID, Input{Title: "Race", DisplayMode: DisplayOnce, Published: true}); err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("publish: %v", err)
	}

	notices, err := notifyStore.List(ctx, everyone, 10, 0)
	if err != nil {
		t.Fatalf("list notices: %v", err)
	}
	if len(notices) != 1 {
		t.Fatalf("%d notices for one publication, want exactly 1", len(notices))
	}
}

// The push of a publish runs after its save has committed. If the announcement
// was unpublished or deleted in between, that change's retract may already have
// run, so the push would be left behind with nothing to retract it later.
func TestNoticePushedAfterUnpublishingIsRetracted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notifyStore := notify.NewStore(f.store.db)
	f.store.Notify = notifyStore
	everyone := user.User{CreatedAt: 0}

	live := f.write(t, Input{Title: "Stale push", Published: true})
	if _, err := f.store.Update(ctx, live.ID, Input{Title: live.Title, Published: false}); err != nil {
		t.Fatalf("unpublish: %v", err)
	}

	f.store.notifyPublished(ctx, live)

	notices, err := notifyStore.List(ctx, everyone, 10, 0)
	if err != nil || len(notices) != 0 {
		t.Fatalf("notices after a late push for an unpublished row = %+v (err %v), want none", notices, err)
	}
}

// The same race for a row that no longer exists at all: a delete's retract can
// run before a late push of the publish that preceded it.
func TestNoticePushedForADeletedAnnouncementIsRetracted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notifyStore := notify.NewStore(f.store.db)
	f.store.Notify = notifyStore
	everyone := user.User{CreatedAt: 0}

	f.store.notifyPublished(ctx, Announcement{ID: "deleted-before-push", Title: "Gone", DisplayMode: DisplayOnce, Published: true})

	notices, err := notifyStore.List(ctx, everyone, 10, 0)
	if err != nil || len(notices) != 0 {
		t.Fatalf("notices for a deleted announcement = %+v (err %v), want none", notices, err)
	}
}

// The bell marks what it showed. An announcement published after its list was
// fetched was never shown, so it must stay unread and keep its popup. A draft
// whose id the bell sent is skipped the same way, and stays unread once it is
// published.
func TestMarkSeenLeavesWhatTheReaderWasNotShown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "reader")

	shown := f.write(t, Input{Title: "Shown", Published: true})
	listed := []string{shown.ID}
	late := f.write(t, Input{Title: "Late", Published: true, DisplayMode: DisplayOnce})
	draft := f.write(t, Input{Title: "Draft", Body: "d"})
	listed = append(listed, draft.ID)

	if err := f.store.MarkSeen(ctx, reader.ID, listed); err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if _, err := f.store.Update(ctx, draft.ID, Input{Title: "Draft", Body: "d", DisplayMode: DisplayOnce, Published: true}); err != nil {
		t.Fatalf("publish draft: %v", err)
	}

	mine, err := f.store.ListFor(ctx, reader.ID)
	if err != nil {
		t.Fatalf("list for reader: %v", err)
	}
	if len(mine) != 3 {
		t.Fatalf("listed %d published announcements, want 3", len(mine))
	}
	for _, record := range mine {
		switch record.ID {
		case shown.ID:
			if !record.Read {
				t.Error("the announcement the reader was shown is still unread")
			}
		case late.ID:
			if record.Read {
				t.Error("an announcement published after the list was fetched was marked read unseen")
			}
		case draft.ID:
			if record.Read {
				t.Error("a draft sent with the list was marked read before it was published")
			}
		}
	}
}

// A long history names more announcements than one statement may bind:
// PostgreSQL refuses more than 65,535 parameters in a statement. The published
// rows at both ends of the list must still be marked, so the list is cut into
// batches and neither end is lost.
func TestMarkSeenBindsALongHistoryInBatches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "reader")
	first := f.write(t, Input{Title: "First", Body: "b", Published: true, DisplayMode: DisplaySilent})
	last := f.write(t, Input{Title: "Last", Body: "b", Published: true, DisplayMode: DisplaySilent})

	const history = 70000
	ids := make([]string, 0, history)
	ids = append(ids, first.ID)
	for len(ids) < history-1 {
		ids = append(ids, "withdrawn")
	}
	ids = append(ids, last.ID)

	if err := f.store.MarkSeen(ctx, reader.ID, ids); err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	mine, err := f.store.ListFor(ctx, reader.ID)
	if err != nil {
		t.Fatalf("list for reader: %v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("listed %d announcements, want 2", len(mine))
	}
	for _, record := range mine {
		if !record.Read {
			t.Errorf("%q was not marked read from a long history", record.Title)
		}
	}
}

func titles(records []Announcement) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, record.Title)
	}
	return out
}

// The body limit counts characters, as the home notice, chat messages and
// feedback do. Counting bytes admitted about a third as much Chinese text as
// the constant's name promises.
func TestBodyLimitCountsCharactersNotBytes(t *testing.T) {
	f := newFixture(t)
	cases := map[string]struct {
		body string
		want error
	}{
		"a full run of CJK characters": {strings.Repeat("界", MaxBodyChars), nil},
		"one CJK character over":       {strings.Repeat("界", MaxBodyChars+1), ErrInvalidBody},
		"a full run of ASCII":          {strings.Repeat("a", MaxBodyChars), nil},
		"one ASCII character over":     {strings.Repeat("a", MaxBodyChars+1), ErrInvalidBody},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := f.store.Create(context.Background(), Input{Title: "Limit", Body: tc.body, DisplayMode: DisplayOnce})
			if tc.want == nil {
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("create = %v, want %v", err, tc.want)
			}
		})
	}
}
