package quota

import (
	"context"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func counterAt(t *testing.T, service *Service, userID string, window Window, at time.Time, anchor int64) (int64, float64) {
	t.Helper()
	var tokens int64
	var credits float64
	err := service.db.QueryRow(context.Background(),
		`SELECT tokens, credits FROM usage_counters
		 WHERE scope_key = ? AND window_kind = ? AND window_start = ?`,
		scopeKey(userID), window, bucketStart(window, at, anchor)).Scan(&tokens, &credits)
	if err != nil {
		return 0, 0
	}
	return tokens, credits
}

// A hold is given back to the counter it came out of.
//
// A generation runs for as long as the model takes, and the windows are one
// minute, five hours, a week. A turn that starts near an edge finishes on the
// other side of it. Releasing at the time of release put the negative delta
// into a bucket that had never been charged, where the floor at zero swallowed
// it — so the bucket that was charged kept a hold nobody was using, and the
// bucket the turn really ran in lost the whole turn.
func TestAHoldIsGivenBackToTheCounterItCharged(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()
	person := account("user-1", "")

	// The window the turn started in, two minutes back: a different minute
	// bucket, and the same five-hour one, which is what makes this a test of
	// the boundary rather than of the arithmetic.
	started := time.Now().Add(-2 * time.Minute)
	held := Estimate{Tokens: 4000, Credits: 4}

	// Charged there, exactly as Reserve would have.
	for _, window := range []Window{WindowTPM, Window5H} {
		if _, err := bump(ctx, db, scopeKey(person.ID), window,
			bucketStart(window, started, person.CreatedAt), 0, held.Tokens, held.Credits); err != nil {
			t.Fatal(err)
		}
	}

	// The turn finishes now, in the next minute, and costs a fraction of it.
	actual := Estimate{Tokens: 120, Credits: 0.12}
	if err := service.Settle(ctx, person, Estimate{}, actual); err != nil {
		t.Fatal(err)
	}
	if err := service.Release(ctx, person.ID, Reservation{at: started, estimate: held, taken: true, anchor: person.CreatedAt}); err != nil {
		t.Fatal(err)
	}

	// The minute it started in is square again.
	if tokens, credits := counterAt(t, service, person.ID, WindowTPM, started, person.CreatedAt); tokens != 0 || credits > 0.001 {
		t.Errorf("the bucket that was charged still holds %d tokens and %v credits", tokens, credits)
	}
	// And the minute it finished in carries the turn, not a zero left behind
	// by a refund that landed in the wrong place.
	if tokens, _ := counterAt(t, service, person.ID, WindowTPM, time.Now(), person.CreatedAt); tokens != actual.Tokens {
		t.Errorf("the bucket the turn ran in holds %d tokens, want the %d it spent", tokens, actual.Tokens)
	}
	// The five-hour window saw both in one bucket, so it nets to the truth.
	if tokens, _ := counterAt(t, service, person.ID, Window5H, time.Now(), person.CreatedAt); tokens != actual.Tokens {
		t.Errorf("the five-hour counter holds %d tokens, want %d", tokens, actual.Tokens)
	}
}

// An account the limits do not apply to has nothing reserved for it, so there
// is nothing to give back — and giving one back anyway drove its counters to
// the floor on every turn. Those counters are what the usage screen reads, so
// an administrator's own reading was never a total, only whatever the last
// answer had cost since the wipe.
func TestAnExemptAccountKeepsItsReading(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	if err := service.settings.Set(ctx, settings.AdminsBypassQuota, "true"); err != nil {
		t.Fatal(err)
	}

	admin := account("admin-1", "")
	admin.Role = user.RoleAdmin

	held, err := service.Reserve(ctx, admin, Estimate{Tokens: 4000, Credits: 4})
	if err != nil {
		t.Fatal(err)
	}
	if held.taken {
		t.Fatal("a reservation was taken for an account the limits do not apply to")
	}

	// Two turns, each settled at what it really cost. The reading is the sum.
	for range 2 {
		if err := service.Settle(ctx, admin, Estimate{}, Estimate{Tokens: 100, Credits: 1}); err != nil {
			t.Fatal(err)
		}
		if err := service.Release(ctx, admin.ID, held); err != nil {
			t.Fatal(err)
		}
	}

	tokens, credits := counterAt(t, service, admin.ID, Window5H, time.Now(), admin.CreatedAt)
	if tokens != 200 {
		t.Errorf("tokens = %d after two turns of 100, want 200", tokens)
	}
	if credits < 1.999 || credits > 2.001 {
		t.Errorf("credits = %v after two turns of 1, want 2", credits)
	}
}
