package quota

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
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
	admin.Role = user.RoleSuperAdmin

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

// A reservation is given back once. Release is called from a deferred closure
// and from the turn that ran it, so nothing stops a second call; a second
// refund subtracts the reservation again and leaves the window with less than
// the turn it belongs to.
func TestAReservationIsGivenBackOnce(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	person := account("user-1", "")
	if _, err := service.Policies().Save(ctx, nil, Policy{Scope: ScopeGlobal, Windows: map[Window]Limits{
		Window5H: limits(true, nil, nil, ptrFloat(1000)),
	}}); err != nil {
		t.Fatal(err)
	}

	// A turn that has already been settled, so the window holds something a
	// double refund could take below.
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 100, Credits: 1}); err != nil {
		t.Fatal(err)
	}
	res, err := service.Reserve(ctx, person, Estimate{Tokens: 4000, Credits: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !res.taken {
		t.Fatal("the enforced window took no reservation to give back")
	}

	for range 2 {
		if err := service.Release(ctx, person.ID, res); err != nil {
			t.Fatal(err)
		}
	}

	tokens, credits := counterAt(t, service, person.ID, Window5H, time.Now(), person.CreatedAt)
	if tokens != 100 {
		t.Errorf("tokens = %d after one reservation released twice, want the 100 that was spent", tokens)
	}
	if credits < 0.999 || credits > 1.001 {
		t.Errorf("credits = %v after one reservation released twice, want 1", credits)
	}
}

// The same reservation given back by several goroutines at once. The sequential
// test above only ever has the second call arrive after the first has committed,
// so it does not exercise two refunds that both pass the check before either one
// lands. Here they start together, and without the guard every refund subtracts
// the same estimate and the window floors at zero.
func TestConcurrentReleasesOfOneReservationGiveItBackOnce(t *testing.T) {
	service, _ := newServiceOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "quota.db")))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	person := account("user-1", "")
	if _, err := service.Policies().Save(ctx, nil, Policy{Scope: ScopeGlobal, Windows: map[Window]Limits{
		Window5H: limits(true, nil, nil, ptrFloat(1000)),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Settle(ctx, person, Estimate{}, Estimate{Tokens: 100, Credits: 1}); err != nil {
		t.Fatal(err)
	}
	res, err := service.Reserve(ctx, person, Estimate{Tokens: 4000, Credits: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !res.taken {
		t.Fatal("the enforced window took no reservation to give back")
	}

	const releases = 16
	start := make(chan struct{})
	errs := make(chan error, releases)
	var wait sync.WaitGroup
	for range releases {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if err := service.Release(ctx, person.ID, res); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("release: %v", err)
	}

	tokens, credits := counterAt(t, service, person.ID, Window5H, time.Now(), person.CreatedAt)
	if tokens != 100 {
		t.Errorf("tokens = %d after %d concurrent releases of one reservation, want the 100 that was spent", tokens, releases)
	}
	if credits < 0.999 || credits > 1.001 {
		t.Errorf("credits = %v after %d concurrent releases of one reservation, want 1", credits, releases)
	}
}
