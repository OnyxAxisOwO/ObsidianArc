package invite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
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

// A partner code is short enough to guess, and an account costs one signup,
// so a claim's wrong answers are counted against the address as well as the
// account. Each guess here comes from a new account, so only the address can
// stop them.
func TestClaimGuessesAreCountedAgainstTheAddress(t *testing.T) {
	f := newFixture(t)
	limiter := auth.NewLimiter()
	handlers := NewHandlers(f.store)
	handlers.ClientIP = func(r *http.Request) string { return r.Header.Get("X-Client") }
	handlers.ReserveGuess = func(ip string) (*auth.Attempt, error) { return limiter.Begin(ip, "") }

	claimFrom := func(username, address string) error {
		account := f.account(t, username)
		req := httptest.NewRequest(http.MethodPost, "/api/profile/invites/claim", strings.NewReader(`{"code":"NOPE0001"}`))
		req.Header.Set("X-Client", address)
		req = req.WithContext(auth.WithUser(req.Context(), account))
		return handlers.claim(httptest.NewRecorder(), req)
	}

	refusedAt := 0
	for i := 0; i < 40 && refusedAt == 0; i++ {
		err := claimFrom("guesser"+strconv.Itoa(i), "203.0.113.7")
		var httpxErr *httpx.Error
		if errors.As(err, &httpxErr) && httpxErr.Status == http.StatusTooManyRequests {
			refusedAt = i + 1
		}
	}
	if refusedAt == 0 {
		t.Fatal("40 wrong codes from one address, each from a new account, were never refused")
	}

	// Another address is not held to the first one's count.
	err := claimFrom("elsewhere", "198.51.100.9")
	var httpxErr *httpx.Error
	if errors.As(err, &httpxErr) && httpxErr.Status == http.StatusTooManyRequests {
		t.Fatalf("a second address was refused after the first one used up its guesses")
	}
	if !errors.As(err, &httpxErr) || httpxErr.Code != "invite_invalid" {
		t.Fatalf("a wrong code from a fresh address gave %v, want invite_invalid", err)
	}
}

// A plugin's decorator that fails used to fail the invites panel for every
// inviter with an invitee, and regenerate answered 500 after its new code had
// already been committed. The core rows are right without the decorator, so
// they are what the panel shows.
func TestAFailingDecoratorKeepsThePanel(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{settings.InvitesUserEnabled: "true"}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	handlers := NewHandlers(f.store)
	handlers.DecorateInvitees("broken", func(context.Context, string, []Invitee) ([]Invitee, error) {
		return nil, errors.New("plugin trapped")
	})

	inviter := f.account(t, "panel-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee := f.account(t, "panel-invitee")
	if _, err := f.db.Exec(ctx,
		`INSERT INTO invite_uses (user_id, code_id, inviter_id, group_days, created_at, rewarded_at, reward_cards, reward_skipped)
		 VALUES (?, ?, ?, 0, ?, 0, 0, '')`,
		invitee.ID, personal.ID, inviter.ID, time.Now().UnixMilli()); err != nil {
		t.Fatalf("record use: %v", err)
	}

	out, err := handlers.payload(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("the panel failed because a decorator did: %v", err)
	}
	invitees, _ := out["invitees"].([]map[string]any)
	if len(invitees) != 1 {
		t.Fatalf("panel lists %d invitees, want the one the core rows hold", len(invitees))
	}
}
