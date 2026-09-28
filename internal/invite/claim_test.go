package invite

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// partnerCode is a shorthand for the shape every claim test needs: a
// claimable code naming a group, minted the same way an administrator would.
func (f *fixture) partnerCode(t *testing.T, code, groupID string, groupDays, groupDaysMax, maxUses int) Code {
	t.Helper()
	created, err := f.store.Create(context.Background(), CreateInput{
		Kind: CodeKindPartner, Count: 1, Code: code, Name: "Test Partner",
		GroupID: groupID, GroupDays: groupDays, GroupDaysMax: groupDaysMax, MaxUses: maxUses,
	})
	if err != nil {
		t.Fatalf("create partner code %s: %v", code, err)
	}
	return created[0]
}

// TestClaimJoinsFromDefaultGroup covers the first branch of Claim's group
// decision: an account sitting in the default registration group (or, as
// here, in no group at all — account() in store_test.go leaves GroupID
// empty) joins the code's group fresh, for exactly the days it grants.
func TestClaimJoinsFromDefaultGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Fresh Join"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "JOINCODE", target.ID, 5, 0, 0)
	account := f.account(t, "join-claimer")

	before := time.Now().UnixMilli()
	result, err := f.store.Claim(ctx, account.ID, code.Code)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if result.GroupID != target.ID || result.GroupName != target.Name || result.Days != 5 {
		t.Fatalf("result = %+v, want group %s (%s) / 5 days", result, target.ID, target.Name)
	}
	if result.ExpiresAt < before+5*86400000 {
		t.Fatalf("expires_at = %d, want at least %d", result.ExpiresAt, before+5*86400000)
	}

	updated, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatalf("read account: %v", err)
	}
	if updated.GroupID != target.ID || updated.GroupExpiresAt != result.ExpiresAt {
		t.Fatalf("account after claim = %+v, want group %s expiring %d", updated, target.ID, result.ExpiresAt)
	}

	got, err := f.store.ByID(ctx, code.ID)
	if err != nil {
		t.Fatalf("read code: %v", err)
	}
	if got.Uses != 1 {
		t.Fatalf("code uses = %d, want 1", got.Uses)
	}
}

// TestClaimExtendsExistingExpiry covers the second branch: an account
// already sitting in the code's own group, on a trial, has the new days
// added on top of whichever is later — now or its current expiry — never
// replaced.
func TestClaimExtendsExistingExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Extend Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	first := f.partnerCode(t, "FIRSTCODE", target.ID, 5, 0, 0)
	second := f.partnerCode(t, "SECONDCODE", target.ID, 3, 0, 0)
	account := f.account(t, "extend-claimer")

	firstResult, err := f.store.Claim(ctx, account.ID, first.Code)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	secondResult, err := f.store.Claim(ctx, account.ID, second.Code)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	want := firstResult.ExpiresAt + 3*86400000
	if secondResult.ExpiresAt != want {
		t.Fatalf("extended expires_at = %d, want %d (first expiry + 3 days)", secondResult.ExpiresAt, want)
	}
	if secondResult.GroupID != target.ID {
		t.Fatalf("extended claim's group = %q, want %q", secondResult.GroupID, target.ID)
	}
}

// TestClaimRefusesPermanentMembershipInSameGroup: an account permanently in
// the code's own group (group_expires_at = 0, the default group's usual
// state) is never given a temporary expiry by a claim — that would be a
// downgrade wearing a reward's clothes.
func TestClaimRefusesPermanentMembershipInSameGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Permanent Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "PERMCODE", target.ID, 5, 0, 0)
	account := f.account(t, "permanent-member")
	groupID := target.ID
	var noExpiry int64
	if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{
		GroupID: &groupID, GroupExpiresAt: &noExpiry,
	}); err != nil {
		t.Fatalf("seat account permanently: %v", err)
	}

	_, err = f.store.Claim(ctx, account.ID, code.Code)
	if !errors.Is(err, ErrGroupConflict) {
		t.Fatalf("claim while permanently in the code's group: %v, want ErrGroupConflict", err)
	}
	// Refused for free: no use spent, and no throttle failure recorded —
	// see refuse() in Claim.
	got, err := f.store.ByID(ctx, code.ID)
	if err != nil {
		t.Fatalf("read code: %v", err)
	}
	if got.Uses != 0 {
		t.Fatalf("code uses = %d after a refused claim, want 0", got.Uses)
	}
}

// TestClaimPermanentGroupGrantsPermanentMembership covers days = 0 (and groupDaysMax = 0):
// claiming a permanent partner code seats the account in the target group with group_expires_at = 0,
// and ResolveMembership does not revert them to the default group.
func TestClaimPermanentGroupGrantsPermanentMembership(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Lifetime Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "LIFETIMECODE", target.ID, 0, 0, 0)
	account := f.account(t, "lifetime-claimer")

	result, err := f.store.Claim(ctx, account.ID, code.Code)
	if err != nil {
		t.Fatalf("claim permanent code: %v", err)
	}
	if result.GroupID != target.ID || result.GroupName != target.Name || result.Days != 0 || result.ExpiresAt != 0 {
		t.Fatalf("result = %+v, want group %s (%s) / days=0 / expires_at=0", result, target.ID, target.Name)
	}

	updated, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatalf("read account: %v", err)
	}
	if updated.GroupID != target.ID || updated.GroupExpiresAt != 0 {
		t.Fatalf("account after claim = %+v, want group %s with group_expires_at=0", updated, target.ID)
	}

	// ResolveMembership must preserve the permanent group, not expire it.
	resolved, err := f.users.ResolveMembership(ctx, nil, updated)
	if err != nil {
		t.Fatalf("resolve membership: %v", err)
	}
	if resolved.GroupID != target.ID || resolved.GroupExpiresAt != 0 {
		t.Fatalf("resolved membership = %+v, want group %s with group_expires_at=0", resolved, target.ID)
	}

	// Also verify that a trial user claiming a permanent code in the same group upgrades to permanent.
	trialAccount := f.account(t, "trial-claimer")
	trialExpiry := time.Now().Add(3 * 24 * time.Hour).UnixMilli()
	if _, err := f.users.UpdateAdminFields(ctx, nil, trialAccount.ID, user.AdminUpdate{
		GroupID: &target.ID, GroupExpiresAt: &trialExpiry,
	}); err != nil {
		t.Fatalf("set trial: %v", err)
	}

	secondCode := f.partnerCode(t, "LIFETIMECODE2", target.ID, 0, 0, 0)
	upgradedResult, err := f.store.Claim(ctx, trialAccount.ID, secondCode.Code)
	if err != nil {
		t.Fatalf("claim permanent code on trial: %v", err)
	}
	if upgradedResult.ExpiresAt != 0 {
		t.Fatalf("upgraded result expires_at = %d, want 0", upgradedResult.ExpiresAt)
	}
	upgradedUser, err := f.users.ByID(ctx, nil, trialAccount.ID)
	if err != nil {
		t.Fatalf("read upgraded user: %v", err)
	}
	if upgradedUser.GroupID != target.ID || upgradedUser.GroupExpiresAt != 0 {
		t.Fatalf("upgraded user = %+v, want permanent (expires_at=0)", upgradedUser)
	}
}

// TestClaimRefusesOtherGroup: an account in some group that has nothing to
// do with the code is refused rather than moved — Claim never replaces a
// membership it did not grant.
func TestClaimRefusesOtherGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Target Group"})
	if err != nil {
		t.Fatalf("create target group: %v", err)
	}
	other, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Other Group"})
	if err != nil {
		t.Fatalf("create other group: %v", err)
	}
	code := f.partnerCode(t, "OTHERGROUPCODE", target.ID, 5, 0, 0)
	account := f.account(t, "other-group-member")
	otherID := other.ID
	if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{GroupID: &otherID}); err != nil {
		t.Fatalf("seat account in the other group: %v", err)
	}

	_, err = f.store.Claim(ctx, account.ID, code.Code)
	if !errors.Is(err, ErrGroupConflict) {
		t.Fatalf("claim while in an unrelated group: %v, want ErrGroupConflict", err)
	}
}

// TestClaimDropsADeletedGroup mirrors TestConsumeDropsADeletedGroup: a code
// naming a group deleted since it was minted still spends, it just grants
// no membership — invite_codes.group_id carries no foreign key, so nothing
// stops an administrator deleting the group out from under it.
func TestClaimDropsADeletedGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Gone Before Claimed"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "DANGLINGCLAIM", target.ID, 5, 0, 0)
	if err := f.groups.Delete(ctx, nil, target.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	account := f.account(t, "dangling-claimer")

	result, err := f.store.Claim(ctx, account.ID, code.Code)
	if err != nil {
		t.Fatalf("claim with a dangling group: %v", err)
	}
	if result.GroupID != "" {
		t.Fatalf("result.GroupID = %q, want empty once the group is gone", result.GroupID)
	}
	got, err := f.store.ByID(ctx, code.ID)
	if err != nil {
		t.Fatalf("read code: %v", err)
	}
	if got.Uses != 1 {
		t.Fatalf("code uses = %d, want 1 (the claim still spends)", got.Uses)
	}
}

// TestClaimRaceExactlyOneWinner is the concurrency bar AGENTS.md sets,
// applied to Claim's own invariant: one claim per account per code. Real
// goroutines, all racing the same account against the same unlimited-use
// code — the account's own row lock should serialise every one of them, so
// exactly one succeeds and the rest are told they already claimed it, not
// throttled and not charged a second use.
func TestClaimRaceExactlyOneWinner(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Race Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "RACECLAIM", target.ID, 5, 0, 0)
	account := f.account(t, "race-claimer")

	const attempts = 20
	var wins, claimed int64
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.store.Claim(ctx, account.ID, code.Code)
			switch {
			case err == nil:
				atomic.AddInt64(&wins, 1)
			case errors.Is(err, ErrClaimed):
				atomic.AddInt64(&claimed, 1)
			default:
				t.Errorf("unexpected error racing to claim: %v", err)
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}
	if claimed != attempts-1 {
		t.Fatalf("told 'already claimed' = %d, want %d", claimed, attempts-1)
	}
	got, err := f.store.ByID(ctx, code.ID)
	if err != nil {
		t.Fatalf("read code: %v", err)
	}
	if got.Uses != 1 {
		t.Fatalf("code uses = %d after the race, want exactly 1", got.Uses)
	}
	var claims int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM invite_claims WHERE code_id = ? AND user_id = ?`,
		code.ID, account.ID).Scan(&claims); err != nil {
		t.Fatalf("count claim rows: %v", err)
	}
	if claims != 1 {
		t.Fatalf("claim rows = %d, want exactly 1", claims)
	}
}

// TestClaimRespectsMaxUsesAndExpiry: the same conditions Consume enforces —
// a use ceiling and an expiry — apply to Claim's own spend, across
// different accounts rather than one account's own lock.
func TestClaimRespectsMaxUsesAndExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Capped Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	capped := f.partnerCode(t, "CAPPEDCODE", target.ID, 5, 0, 1)
	first := f.account(t, "capped-first")
	if _, err := f.store.Claim(ctx, first.ID, capped.Code); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	second := f.account(t, "capped-second")
	if _, err := f.store.Claim(ctx, second.ID, capped.Code); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claim past max_uses: %v, want ErrInvalid", err)
	}

	expired := f.insertCode(t, Code{
		Code: "EXPIREDCLAIM", Kind: CodeKindPartner, AllowExisting: true,
		GroupID: target.ID, GroupDays: 5, ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(),
	})
	account := f.account(t, "expired-claimer")
	if _, err := f.store.Claim(ctx, account.ID, expired.Code); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claim an expired code: %v, want ErrInvalid", err)
	}
}

// TestClaimRefusesPersonalAndNonClaimableCodes: Claim only ever spends a
// batch or partner code with allow_existing set — a personal code (by kind)
// and an ordinary batch that never opted in (allow_existing defaults to
// false) both read as the same flat ErrInvalid a wrong guess does.
func TestClaimRefusesPersonalAndNonClaimableCodes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.account(t, "personal-owner")
	personal, err := f.store.PersonalCode(ctx, owner.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	claimer := f.account(t, "would-be-claimer")
	if _, err := f.store.Claim(ctx, claimer.ID, personal.Code); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claim a personal code: %v, want ErrInvalid", err)
	}

	batch, err := f.store.Create(ctx, CreateInput{Count: 1})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if _, err := f.store.Claim(ctx, claimer.ID, batch[0].Code); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claim a batch code with allow_existing off: %v, want ErrInvalid", err)
	}
}

// TestClaimThrottlesGuesses: ten wrong codes in an hour is the budget: the
// eleventh is refused outright with a Retry-After, without this package
// touching auth.Limiter's in-memory buckets (see invite_claim_throttle).
func TestClaimThrottlesGuesses(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account := f.account(t, "guesser")

	for i := 0; i < maxClaimFailures; i++ {
		if _, err := f.store.Claim(ctx, account.ID, "NOSUCHCODE"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("guess %d: %v, want ErrInvalid", i, err)
		}
	}

	_, err := f.store.Claim(ctx, account.ID, "NOSUCHCODE")
	var throttled *ClaimThrottled
	if !errors.As(err, &throttled) {
		t.Fatalf("guess %d: %v, want *ClaimThrottled", maxClaimFailures, err)
	}
	if throttled.RetryAfter <= 0 || throttled.RetryAfter > claimFailureWindow {
		t.Fatalf("retry after = %s, want within (0, %s]", throttled.RetryAfter, claimFailureWindow)
	}

	// A real code is refused the same way while blocked — the throttle is a
	// door, not just a slower guess.
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Throttled Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "THROTTLECODE", target.ID, 5, 0, 0)
	if _, err := f.store.Claim(ctx, account.ID, code.Code); !errors.As(err, &throttled) {
		t.Fatalf("claim a good code while throttled: %v, want *ClaimThrottled", err)
	}

	// Claimed and group-conflict refusals never count against the budget:
	// a legitimate account retrying a real state should not be locked out
	// the way a string of wrong guesses is.
	other := f.account(t, "non-guesser")
	fresh := f.partnerCode(t, "NONGUESSCODE", target.ID, 5, 0, 0)
	if _, err := f.store.Claim(ctx, other.ID, fresh.Code); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	for i := 0; i < maxClaimFailures+2; i++ {
		if _, err := f.store.Claim(ctx, other.ID, fresh.Code); !errors.Is(err, ErrClaimed) {
			t.Fatalf("repeat claim %d: %v, want ErrClaimed (never throttled)", i, err)
		}
	}
}

// TestClaimRefusesAnAccountThatAlreadyRegisteredThroughTheCode: "one claim
// per account per code" has to hold against invite_uses, not only against
// invite_claims — otherwise an account that registered through a partner
// code (which seats invite_uses, never invite_claims) could turn straight
// around and POST the same code to Claim and be topped up a second time,
// spending a second use of a code meant for one grant per account.
func TestClaimRefusesAnAccountThatAlreadyRegisteredThroughTheCode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Double Dip Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "DOUBLEDIP", target.ID, 5, 0, 0)

	// Mirrors auth.Service.Register's real sequence rather than the
	// personal-code-oriented registerThrough helper (which never sets a
	// group, since a personal code never grants one): Consume, Create with
	// GroupID = grant.GroupID, RecordUse, then group_expires_at from
	// grant.GroupDays — see auth.Service.applyInvite.
	var alice user.User
	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		grant, err := f.store.Consume(ctx, tx, code.Code, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		alice, err = f.users.Create(ctx, tx, user.CreateInput{
			Username: "double-dip-alice", PasswordHash: "x", Role: user.RoleUser,
			Status: user.StatusActive, GroupID: grant.GroupID, SignupIP: "203.0.113.200",
		})
		if err != nil {
			return err
		}
		if err := f.store.RecordUse(ctx, tx, grant.CodeID, alice.ID, grant.OwnerID, grant.GroupDays); err != nil {
			return err
		}
		expiresAt := time.Now().Add(time.Duration(grant.GroupDays) * 24 * time.Hour).UnixMilli()
		if _, err := tx.Exec(ctx, `UPDATE users SET group_expires_at = ? WHERE id = ?`, expiresAt, alice.ID); err != nil {
			return err
		}
		alice.GroupExpiresAt = expiresAt
		return nil
	})
	if err != nil {
		t.Fatalf("register alice through %s: %v", code.Code, err)
	}

	seated, err := f.users.ByID(ctx, nil, alice.ID)
	if err != nil {
		t.Fatalf("read seated account: %v", err)
	}
	if seated.GroupID != target.ID || seated.GroupExpiresAt == 0 {
		t.Fatalf("account after registration = %+v, want seated in %s with an expiry", seated, target.ID)
	}
	firstExpiry := seated.GroupExpiresAt

	if _, err := f.store.Claim(ctx, alice.ID, code.Code); !errors.Is(err, ErrClaimed) && !errors.Is(err, ErrInvalid) {
		t.Fatalf("claim after registering through the same code: %v, want ErrClaimed or ErrInvalid", err)
	}

	after, err := f.users.ByID(ctx, nil, alice.ID)
	if err != nil {
		t.Fatalf("read account after claim attempt: %v", err)
	}
	if after.GroupExpiresAt != firstExpiry {
		t.Fatalf("group_expires_at = %d after claim attempt, want unchanged %d (no second grant)", after.GroupExpiresAt, firstExpiry)
	}

	got, err := f.store.ByID(ctx, code.ID)
	if err != nil {
		t.Fatalf("read code: %v", err)
	}
	if got.Uses != 1 {
		t.Fatalf("code uses = %d after claim attempt, want 1 (no second spend)", got.Uses)
	}
}
