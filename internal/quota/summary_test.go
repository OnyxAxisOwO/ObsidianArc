package quota

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The administrator's overview reads the counters of the accounts on one page,
// not of the instance. A page that names two accounts must not come back with
// every row either of them has, and the rows it does read must be the ones
// SummaryFor reads for each account.
func TestSummariesForReadsOnlyTheAccountsItIsAskedAbout(t *testing.T) {
	_, db := newService(t)
	ctx := context.Background()
	now := time.Now()

	mine := account("user-mine", "")
	other := account("user-other", "")
	seed := func(u user.User, at time.Time, tokens int64) {
		t.Helper()
		if _, err := bump(ctx, db, scopeKey(u.ID), Window5H, bucketStart(Window5H, at, u.CreatedAt), 0, tokens, 0); err != nil {
			t.Fatal(err)
		}
	}
	seed(mine, now, 10)
	seed(other, now, 20)
	// Older buckets, six hours back, which is past the five-hour floor. The
	// account being read has one too, so the floor is exercised for it and not
	// only for the scope that is never read.
	seed(mine, now.Add(-6*time.Hour), 30)
	seed(other, now.Add(-6*time.Hour), 30)

	counters := map[counterSlot]counter{}
	if err := readCurrentCounters(ctx, db, []user.User{mine}, now, counters); err != nil {
		t.Fatal(err)
	}
	if len(counters) != 1 {
		t.Fatalf("read %d counter rows for one account, want its current five-hour bucket only", len(counters))
	}
	if got := counters[counterSlot{scopeKey(mine.ID), Window5H, bucketStart(Window5H, now, mine.CreatedAt)}]; got.Tokens != 10 {
		t.Errorf("the account's current bucket holds %d tokens, want 10", got.Tokens)
	}
	if _, ok := counters[counterSlot{scopeKey(mine.ID), Window5H, bucketStart(Window5H, now.Add(-6*time.Hour), mine.CreatedAt)}]; ok {
		t.Error("the account's bucket from six hours ago was read past the five-hour floor")
	}
}

// SummariesFor is a batch of SummaryFor. Accounts on both sides of a chunk
// boundary must come out the same as they do one at a time.
func TestSummariesForAgreesWithSummaryForAcrossChunks(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := service.Policies().Save(ctx, nil, Policy{Scope: ScopeGlobal, Windows: map[Window]Limits{
		Window5H:   limits(true, nil, nil, ptrFloat(100)),
		WindowWeek: limits(false, nil, ptrInt(5000), nil),
	}}); err != nil {
		t.Fatal(err)
	}

	accounts := make([]user.User, 0, summaryKeyChunk+20)
	for i := range summaryKeyChunk + 20 {
		u := account(fmt.Sprintf("user-%04d", i), "")
		u.CreatedAt = now.Add(-time.Duration(i) * 37 * time.Minute).UnixMilli()
		accounts = append(accounts, u)
	}
	for _, i := range []int{0, summaryKeyChunk - 1, summaryKeyChunk, summaryKeyChunk + 19} {
		u := accounts[i]
		for _, window := range []Window{Window5H, WindowWeek} {
			if _, err := bump(ctx, db, scopeKey(u.ID), window, bucketStart(window, now, u.CreatedAt),
				0, int64(i+1), float64(i)+0.5); err != nil {
				t.Fatal(err)
			}
		}
	}

	summaries, err := service.SummariesFor(ctx, accounts)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 1, summaryKeyChunk - 1, summaryKeyChunk, summaryKeyChunk + 19} {
		u := accounts[i]
		want, err := service.SummaryFor(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := summaries[u.ID]
		if !ok {
			t.Fatalf("account %d is missing from the batch", i)
		}
		if got.Unlimited != want.Unlimited || len(got.Windows) != len(want.Windows) {
			t.Fatalf("account %d: batch says unlimited=%v with %d windows, one at a time says %v with %d",
				i, got.Unlimited, len(got.Windows), want.Unlimited, len(want.Windows))
		}
		for j := range want.Windows {
			g, w := got.Windows[j], want.Windows[j]
			if g.Kind != w.Kind || g.Enforced != w.Enforced || g.UsedTokens != w.UsedTokens ||
				g.UsedRequests != w.UsedRequests || g.UsedCredits != w.UsedCredits {
				t.Errorf("account %d window %s: batch %+v, one at a time %+v", i, w.Kind, g, w)
			}
		}
	}
}
