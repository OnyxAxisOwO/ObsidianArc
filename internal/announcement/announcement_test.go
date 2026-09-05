package announcement

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
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

func titles(records []Announcement) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, record.Title)
	}
	return out
}
