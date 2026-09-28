package invite

import (
	"context"
	"strconv"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// TestProfilePayloadFields exercises Handlers.payload directly — the shape
// GET /api/profile/invites and POST .../regenerate both answer with —
// checking every field the reward-every-N contract added, and that the
// claim box's own numbers (reward_every, reward_cards, reward_card_days,
// next_reward_in) are present even while this account's personal code is
// switched off, since that box works from the instance settings alone.
func TestProfilePayloadFields(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	handlers := NewHandlers(f.store)
	inviter := f.account(t, "payload-inviter")

	// Off: the claim box's own numbers still read back correctly, and
	// nothing about an account's own code is asked for.
	off, err := handlers.payload(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("payload while off: %v", err)
	}
	if off["enabled"] != false || off["code"] != "" || off["counted"] != 0 {
		t.Fatalf("payload while off = %+v", off)
	}
	if off["reward_every"] != 1 || off["reward_cards"] != 0 || off["next_reward_in"] != 0 {
		t.Fatalf("payload while off (reward fields) = %+v", off)
	}

	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "3",
		settings.InvitesRewardCardDays: "20", settings.InvitesUserLimit: "0",
		settings.InvitesRewardEvery: "4",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	for i := 0; i < 5; i++ {
		invitee := f.registerThrough(t, personal.Code, "203.0.113."+strconv.Itoa(180+i))
		if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
			t.Fatalf("reward %d: %v", i, err)
		}
	}

	on, err := handlers.payload(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if on["enabled"] != true || on["code"] != personal.Code {
		t.Fatalf("payload enabled/code = %+v", on)
	}
	if on["used"] != 5 || on["counted"] != 5 {
		t.Fatalf("payload used/counted = %+v, want 5 and 5", on)
	}
	if on["reward_cards"] != 3 || on["reward_card_days"] != 20 || on["reward_every"] != 4 {
		t.Fatalf("payload reward settings = %+v", on)
	}
	// counted = 5, every = 4: one milestone already paid (the 4th), one more
	// (the 8th) needs 3 more — 4 - (5 % 4) = 3.
	if on["next_reward_in"] != 3 {
		t.Fatalf("next_reward_in = %v, want 3", on["next_reward_in"])
	}

	invitees, ok := on["invitees"].([]map[string]any)
	if !ok || len(invitees) != 5 {
		t.Fatalf("invitees = %+v, want 5 rows", on["invitees"])
	}
	var milestoneRows int
	for _, row := range invitees {
		if row["counted"] != true {
			t.Errorf("invitee row not counted: %+v", row)
		}
		if row["reward_skipped"] != "" {
			t.Errorf("invitee row unexpectedly skipped: %+v", row)
		}
		if cards, _ := row["reward_cards"].(int); cards > 0 {
			milestoneRows++
			if cards != 3 {
				t.Errorf("milestone row reward_cards = %v, want 3", row["reward_cards"])
			}
		}
	}
	if milestoneRows != 1 {
		t.Fatalf("rows carrying cards = %d, want 1", milestoneRows)
	}
}

func TestRegeneratePreservesHistoricalTotalUsedInPayload(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	handlers := NewHandlers(f.store)
	inviter := f.account(t, "regen-history-inviter")

	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1",
		settings.InvitesUserLimit: "10",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	firstCode, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("first personal code: %v", err)
	}
	for i := 0; i < 3; i++ {
		invitee := f.registerThrough(t, firstCode.Code, "203.0.113."+strconv.Itoa(190+i))
		if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
			t.Fatalf("reward: %v", err)
		}
	}

	// Regenerate a new code
	secondCode, err := f.store.Regenerate(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if secondCode.Code == firstCode.Code {
		t.Fatal("code did not change")
	}

	payload, err := handlers.payload(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["code"] != secondCode.Code {
		t.Fatalf("payload code = %v, want %s", payload["code"], secondCode.Code)
	}
	// "used" must reflect all historical invite_uses under the inviter, matching the user_limit basis
	if payload["used"] != 3 {
		t.Fatalf("payload used = %v, want 3 (historical uses across all codes)", payload["used"])
	}
	invitees, ok := payload["invitees"].([]map[string]any)
	if !ok || len(invitees) != 3 {
		t.Fatalf("payload invitees = %+v, want 3 historical invitees preserved", payload["invitees"])
	}
}
