package quota

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The single read and the batch read both stack an account's levels through
// accountLayers. A group level counts only for an account in a group, and a
// missing level adds nothing.
func TestAccountLayersStacksGlobalGroupThenUser(t *testing.T) {
	global := Policy{Scope: ScopeGlobal}
	group := Policy{Scope: ScopeGroup, ScopeID: "group-a"}
	own := Policy{Scope: ScopeUser, ScopeID: "user-a"}

	stacked := accountLayers(true, &global, &group, &own)
	if len(stacked) != 3 || stacked[0].Scope != ScopeGlobal || stacked[1].Scope != ScopeGroup || stacked[2].Scope != ScopeUser {
		t.Errorf("a grouped account stacked %+v, want global, group, then user", stacked)
	}
	if got := accountLayers(false, &global, &group, &own); len(got) != 2 || got[1].Scope != ScopeUser {
		t.Errorf("an ungrouped account stacked %+v, want global then user, no group", got)
	}
	if got := accountLayers(true, nil, nil, &own); len(got) != 1 || got[0].Scope != ScopeUser {
		t.Errorf("a level-less account stacked %+v, want its own level alone", got)
	}
}

// The batch summary must give each account the limits SummaryFor gives it when
// the account has a group level and its own level as well as the global one.
// The two readers take their levels from different places, so only this check
// keeps them from drifting apart.
func TestSummariesForStacksGroupAndUserLikeSummaryFor(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	save := func(policy Policy) {
		t.Helper()
		if _, err := service.Policies().Save(ctx, nil, policy); err != nil {
			t.Fatal(err)
		}
	}
	save(Policy{Scope: ScopeGlobal, Windows: map[Window]Limits{
		WindowWeek: limits(false, nil, ptrInt(5000), nil),
	}})
	save(Policy{Scope: ScopeGroup, ScopeID: "group-a", Windows: map[Window]Limits{
		WindowWeek: limits(false, nil, ptrInt(2000), nil),
	}})
	save(Policy{Scope: ScopeUser, ScopeID: "grouped-and-limited", Windows: map[Window]Limits{
		Window5H: limits(true, nil, nil, ptrFloat(50)),
	}})

	accounts := []user.User{
		account("grouped-and-limited", "group-a"),
		account("grouped-only", "group-a"),
		account("ungrouped", ""),
	}
	for i := range accounts {
		accounts[i].CreatedAt = time.Now().Add(-time.Duration(i+1) * time.Hour).UnixMilli()
	}
	summaries, err := service.SummariesFor(ctx, accounts)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range accounts {
		want, err := service.SummaryFor(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := summaries[u.ID]
		if !ok {
			t.Fatalf("%s is missing from the batch", u.ID)
		}
		if got.Unlimited != want.Unlimited || len(got.Windows) != len(want.Windows) {
			t.Fatalf("%s: batch unlimited=%v with %d windows, one at a time unlimited=%v with %d",
				u.ID, got.Unlimited, len(got.Windows), want.Unlimited, len(want.Windows))
		}
		for j := range want.Windows {
			g, w := got.Windows[j], want.Windows[j]
			if g.Kind != w.Kind || g.Enforced != w.Enforced ||
				!reflect.DeepEqual(g.LimitRequests, w.LimitRequests) ||
				!reflect.DeepEqual(g.LimitTokens, w.LimitTokens) ||
				!reflect.DeepEqual(g.LimitCredits, w.LimitCredits) {
				t.Errorf("%s window %s: batch %+v, one at a time %+v", u.ID, w.Kind, g, w)
			}
		}
	}
}

// A reset row that does not parse reads as no reset at all. It is logged, not
// refused: a refused read would stop every reservation on the instance.
func TestAGarbledGlobalResetReadsAsNeverReset(t *testing.T) {
	_, db := newService(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
		globalResetKey, "soon", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	resetAt, err := globalResetAt(ctx, db)
	if err != nil || resetAt != 0 {
		t.Fatalf("a garbled reset read as %d (%v), want 0 and no error", resetAt, err)
	}
}
