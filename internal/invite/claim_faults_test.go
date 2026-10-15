package invite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A CHECK, NOT NULL or foreign-key failure says "constraint" too; reading it
// as a duplicate reported a bad row as "that code already exists".
func TestIsUniqueOnlyMatchesAUniqueClash(t *testing.T) {
	for message, want := range map[string]bool{
		"CHECK constraint failed: uses >= 0":                      false,
		"NOT NULL constraint failed: invite_codes.code":           false,
		"FOREIGN KEY constraint failed":                           false,
		"UNIQUE constraint failed: invite_codes.code":             true,
		`duplicate key value violates unique constraint "x_pkey"`: true,
	} {
		if got := isUnique(errors.New(message)); got != want {
			t.Errorf("isUnique(%q) = %v, want %v", message, got, want)
		}
	}
}

// A repeat claim is refused by the statement, not by an error the caller then
// has to catch: PostgreSQL aborts a transaction on a failed INSERT, and the
// refund that follows would then fail too.
func TestInsertClaimReportsARepeatWithoutFailingTheTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Repeat Join"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.partnerCode(t, "REPEATCODE", target.ID, 0, 0, 0)
	account := f.account(t, "repeat-claimer")
	if _, err := f.store.Claim(ctx, account.ID, code.Code); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		inserted, err := insertClaim(ctx, tx, code.ID, account.ID, 0, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		if inserted {
			t.Error("a repeat claim reported itself as new")
		}
		var rows int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM invite_claims WHERE code_id = ? AND user_id = ?`,
			code.ID, account.ID).Scan(&rows); err != nil {
			return err
		}
		if rows != 1 {
			t.Errorf("claim rows = %d, want 1", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction after a repeat claim: %v", err)
	}
}

// The reward commits before the inviter's notice is pushed. A failed push is
// logged; it must not come back as a failed reward, which the caller logs
// as one, while the reward itself has been granted and cannot be granted
// again.
func TestAFailedInviterNoticeDoesNotFailTheReward(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled:    "true",
		settings.InvitesRewardCards:    "1",
		settings.InvitesRewardCardDays: "30",
		settings.InvitesUserLimit:      "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "notice-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee := f.registerThrough(t, personal.Code, "203.0.113.9")

	if _, err := f.db.Exec(ctx, `DROP TABLE notifications`); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}
	if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("a failed inviter notice came back as a failed reward: %v", err)
	}

	var rewardedAt int64
	if err := f.db.QueryRow(ctx,
		`SELECT rewarded_at FROM invite_uses WHERE user_id = ?`, invitee.ID).Scan(&rewardedAt); err != nil {
		t.Fatalf("read use: %v", err)
	}
	if rewardedAt == 0 {
		t.Fatal("the reward was not recorded as paid")
	}
}
