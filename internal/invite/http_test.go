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
