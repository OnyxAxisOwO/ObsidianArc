package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A request speaks for the account that made it. A signed-in account that is
// not an administrator may end only its own account, and an administrator may
// end an ordinary account only with the users grant, which is read again under
// the lock: a grant taken away since the session began is not used.
func TestAPackageActsForAnOrdinaryAccountOnlyAsTheRequestMayAct(t *testing.T) {
	r := newAccountRig(t)
	ctx := context.Background()
	member := accountOf(t, r, "member", user.RoleUser)
	other := accountOf(t, r, "other", user.RoleUser)
	granted := accountOf(t, r, "granted", user.RoleAdmin)
	ungranted := accountOf(t, r, "ungranted", user.RoleAdmin)
	if _, err := r.db.Exec(ctx, `UPDATE users SET admin_permissions = ? WHERE id = ?`, `["users"]`, granted.ID); err != nil {
		t.Fatal(err)
	}

	if code := hostCode(r.userCallAs("users.set_status", other.ID, member)); code != "permission_denied" {
		t.Errorf("a signed-in account suspending another: %q", code)
	}
	if status, _ := r.statusOf(t, other.ID); status != "active" {
		t.Fatalf("another account was changed by a request it did not make: %q", status)
	}
	if code := hostCode(r.userCallAs("users.set_status", other.ID, ungranted)); code != "permission_denied" {
		t.Errorf("an administrator without the users grant: %q", code)
	}
	if err := r.userCallAs("users.set_status", member.ID, member); err != nil {
		t.Fatalf("an account acting on itself: %v", err)
	}
	if err := r.userCallAs("users.set_status", other.ID, granted); err != nil {
		t.Fatalf("an administrator with the users grant: %v", err)
	}
	if status, _ := r.statusOf(t, other.ID); status != "disabled" {
		t.Errorf("status = %q", status)
	}

	victim := accountOf(t, r, "victim", user.RoleUser)
	if _, err := r.db.Exec(ctx, `UPDATE users SET admin_permissions = ? WHERE id = ?`, `[]`, granted.ID); err != nil {
		t.Fatal(err)
	}
	if code := hostCode(r.userCallAs("users.delete", victim.ID, granted)); code != "permission_denied" {
		t.Errorf("an administrator whose grant was taken away: %q", code)
	}
}

// Sessions, cards and rewards for an account are the same rule: the request has
// to be the account's own, or an administrator's with the grant the backoffice
// asks for the change.
func TestAPackageSessionsCardsAndRewardsForAnotherAccountNeedTheGrant(t *testing.T) {
	r := newRig(t)
	member := accountOf(t, r, "member", user.RoleUser)
	other := accountOf(t, r, "other", user.RoleUser)
	man := &arcx.Manifest{Permissions: []string{arcx.PermSessions, arcx.PermCards, arcx.PermRewards}}
	host := r.manager.hostFunc(&loaded{name: "rewarder", pkg: &arcx.Package{Manifest: man}})
	call := &wasm.Call{Ctx: context.Background(), Info: wasm.CallInfo{Actor: actorOf(member)}}

	for op, raw := range map[string]string{
		"sessions.revoke_user":   `{"user_id":"` + other.ID + `"}`,
		"cards.revoke_available": `{"user_id":"` + other.ID + `","count":1}`,
		"rewards.cards":          `{"user_id":"` + other.ID + `","count":1}`,
		"rewards.bonus":          `{"user_id":"` + other.ID + `","bar_id":"bar","amount":1}`,
	} {
		if _, err := host(call, op, json.RawMessage(raw)); hostCode(err) != "permission_denied" {
			t.Errorf("%s for another account by a signed-in account: %v", op, err)
		}
	}
}

// A Manager with no host has no session store to end. Suspending an account
// there must still land, not dereference the store the way it did before
// set_status checked for a host.
func TestASuspensionWithNoHostStillSuspends(t *testing.T) {
	r := newRig(t)
	if r.manager.host != nil {
		t.Fatal("the test needs a manager with no host")
	}
	member := accountOf(t, r, "member", user.RoleUser)
	if err := r.userCall("users.set_status", member.ID, nil); err != nil {
		t.Fatalf("suspending with no host: %v", err)
	}
	if status, _ := r.statusOf(t, member.ID); status != "disabled" {
		t.Fatalf("status = %q", status)
	}
}

// The grant is checked before the account is looked up. Answering a caller
// without the grant with "not found", or with success, would tell it which
// accounts exist; it gets the same refusal for an account that is not there as
// for one that is. An administrator with the grant still gets the idempotent
// success a sweep gets for an account already gone.
func TestAPackageEndingAnAbsentAccountNeedsTheGrantToo(t *testing.T) {
	r := newAccountRig(t)
	ctx := context.Background()
	member := accountOf(t, r, "member", user.RoleUser)
	granted := accountOf(t, r, "granted", user.RoleAdmin)
	if _, err := r.db.Exec(ctx, `UPDATE users SET admin_permissions = ? WHERE id = ?`, `["users"]`, granted.ID); err != nil {
		t.Fatal(err)
	}
	const absent = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

	if code := hostCode(r.userCallAs("users.delete", absent, member)); code != "permission_denied" {
		t.Errorf("a signed-in account deleting an absent account: %q", code)
	}
	if code := hostCode(r.userCallAs("users.set_status", absent, member)); code != "permission_denied" {
		t.Errorf("a signed-in account suspending an absent account: %q", code)
	}
	if err := r.userCallAs("users.delete", absent, granted); err != nil {
		t.Errorf("an administrator with the grant deleting an absent account: %v", err)
	}
}

// The grant a package acts under is read under the same row lock as the change
// it makes. A revocation still uncommitted when the call starts is waited for,
// and the call then sees the grant gone. Read before the lock, the grant would
// be the one the session began with, and the revocation would only land after
// the sessions were already ended.
func TestAGrantRevokedWhileASessionRevokeIsInFlightIsHonouredWhenItLands(t *testing.T) {
	r := newAccountRig(t)
	ctx := context.Background()
	other := accountOf(t, r, "other", user.RoleUser)
	granted := accountOf(t, r, "granted", user.RoleAdmin)
	if _, err := r.db.Exec(ctx, `UPDATE users SET admin_permissions = ? WHERE id = ?`, `["users"]`, granted.ID); err != nil {
		t.Fatal(err)
	}
	host := r.manager.hostFunc(&loaded{name: "sessioner", pkg: &arcx.Package{Manifest: &arcx.Manifest{Permissions: []string{arcx.PermSessions}}}})
	call := &wasm.Call{Ctx: ctx, Info: wasm.CallInfo{Actor: actorOf(granted)}}
	raw := json.RawMessage(`{"user_id":"` + other.ID + `"}`)

	revoke, err := r.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revoke.Exec(ctx, `UPDATE users SET admin_permissions = ? WHERE id = ?`, `[]`, granted.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := host(call, "sessions.revoke_user", raw)
		done <- err
	}()
	select {
	case err := <-done:
		_ = revoke.Rollback()
		t.Fatalf("the call finished while the revocation was in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := revoke.Commit(); err != nil {
		t.Fatal(err)
	}
	if code := hostCode(<-done); code != "permission_denied" {
		t.Fatalf("a grant revoked before the change landed was still honoured: %q", code)
	}
}
