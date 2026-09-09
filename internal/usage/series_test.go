package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func seriesFixture(t *testing.T) (*Store, user.User) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "usage.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	membership, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewStore(db)
	account, err := users.Create(ctx, nil, user.CreateInput{
		Username: "spender", PasswordHash: "x", GroupID: membership.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db), account
}

// The bucketing moved from a loop in Go into a GROUP BY, so what it produces
// is worth pinning: the boundaries, the sums, and the fact that a failed turn
// is counted as one.
func TestSeriesBucketsByTheStepItIsGiven(t *testing.T) {
	store, account := seriesFixture(t)
	ctx := context.Background()

	const hour = int64(3600_000)
	// Two turns in the first hour, one in the third. The offsets inside each
	// hour are deliberate: the bucket is the hour, not the timestamp.
	write := func(at int64, input, output int, status Status) {
		t.Helper()
		if err := store.Write(ctx, Record{
			UserID: account.ID, GroupID: account.GroupID, RequestID: "r" + time.Duration(at).String(),
			InputTokens: input, OutputTokens: output, TotalTokens: input + output,
			Credits: 1.5, Status: status, StartedAt: at, FinishedAt: at + 10,
		}); err != nil {
			t.Fatal(err)
		}
	}

	write(0, 10, 20, StatusOK)
	write(hour-1, 5, 5, StatusError)
	write(2*hour+7, 1, 2, StatusOK)

	points, err := store.Series(ctx, Filter{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d buckets, want 2: %+v", len(points), points)
	}

	first, third := points[0], points[1]
	if first.At != 0 || third.At != 2*hour {
		t.Errorf("bucket starts are %d and %d, want 0 and %d", first.At, third.At, 2*hour)
	}
	if first.Requests != 2 || third.Requests != 1 {
		t.Errorf("request counts are %d and %d, want 2 and 1", first.Requests, third.Requests)
	}
	if first.InputTokens != 15 || first.OutputTokens != 25 || first.TotalTokens != 40 {
		t.Errorf("first bucket totals = %d/%d/%d, want 15/25/40",
			first.InputTokens, first.OutputTokens, first.TotalTokens)
	}
	if first.Credits != 3 {
		t.Errorf("first bucket credits = %v, want 3", first.Credits)
	}
	// One of the two failed; the other did not.
	if first.Errors != 1 {
		t.Errorf("first bucket errors = %d, want 1", first.Errors)
	}
	if third.Errors != 0 {
		t.Errorf("third bucket errors = %d, want 0", third.Errors)
	}
}

// The window and the other filters still narrow it, and they share the
// statement with the two placeholders the select list now carries — which is
// the way an argument-order mistake would show up.
func TestSeriesStillHonoursItsFilter(t *testing.T) {
	store, account := seriesFixture(t)
	ctx := context.Background()

	const hour = int64(3600_000)
	for i, at := range []int64{0, hour, 2 * hour} {
		if err := store.Write(ctx, Record{
			UserID: account.ID, RequestID: string(rune('a' + i)),
			InputTokens: 1, TotalTokens: 1, Status: StatusOK,
			StartedAt: at, FinishedAt: at + 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	points, err := store.Series(ctx, Filter{Since: hour}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d buckets, want the 2 inside the window: %+v", len(points), points)
	}
	if points[0].At != hour {
		t.Errorf("first bucket = %d, want %d", points[0].At, hour)
	}

	none, err := store.Series(ctx, Filter{UserID: "nobody"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("an account with no turns produced %d buckets", len(none))
	}
}
