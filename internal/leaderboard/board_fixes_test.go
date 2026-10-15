package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The board's profiles used to be fetched for the top 50 only, so any place
// past 50 on a board set to 60 or 100 showed as anonymous.
func TestEveryPlaceTheBoardShowsIsNamed(t *testing.T) {
	h, usageStore, db, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil
	ctx := context.Background()
	if err := h.settings.Set(ctx, settings.LeaderboardSize, "60"); err != nil {
		t.Fatal(err)
	}

	users := user.NewStore(db)
	for i := 0; i < 60; i++ {
		member, err := users.Create(ctx, nil, user.CreateInput{
			Username:     fmt.Sprintf("member%02d", i),
			Nickname:     fmt.Sprintf("Member %02d", i),
			PasswordHash: "hash",
			Role:         user.RoleUser,
			GroupID:      adminUser.GroupID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := usageStore.Write(ctx, usage.Record{
			UserID:       member.ID,
			ModelID:      "model-1",
			InputTokens:  1000 + i,
			OutputTokens: 0,
			TotalTokens:  1000 + i,
			Status:       usage.StatusOK,
			StartedAt:    time.Now().Add(-time.Hour).UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := boardRequest(adminUser, "10.0.0.1:1")
	w := httptest.NewRecorder()
	if err := h.board(w, req); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Accounts []Entry `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 60 {
		t.Fatalf("board showed %d places, want 60", len(got.Accounts))
	}
	for _, entry := range got.Accounts {
		if entry.Name == "" {
			t.Fatalf("place %d is anonymous; every place on a 60-place board has a nickname", entry.Rank)
		}
	}
}

// The limit is kept per /64, as every other per-address limit is. A subscriber
// who rotates addresses inside one prefix used to get a fresh bucket each time.
func TestAnIPv6SubscriberSharesOneLeaderboardBudget(t *testing.T) {
	h, _, _, adminUser := setupLeaderboardFixture(t)
	h.ClientIP = func(r *http.Request) string { return r.Header.Get("X-Client") }
	// No refill: with the production rate of two a second, a stall of about half
	// a second between the fifth and sixth request hands the sixth a token.
	h.Limiter = httpx.NewTokenBucketLimiter(0, 5)

	for i := 1; i <= 5; i++ {
		req := boardRequest(adminUser, "")
		req.Header.Set("X-Client", "2001:db8:0:1::"+strconv.Itoa(i))
		if err := h.board(httptest.NewRecorder(), req); err != nil {
			t.Fatalf("address %d within the burst was refused: %v", i, err)
		}
	}

	req := boardRequest(adminUser, "")
	req.Header.Set("X-Client", "2001:db8:0:1::ffff")
	err := h.board(httptest.NewRecorder(), req)
	var httpxErr *httpx.Error
	if !errorsAs(err, &httpxErr) || httpxErr.Status != http.StatusTooManyRequests {
		t.Fatalf("a sixth address in the same /64 gave %v, want 429", err)
	}

	// Another prefix is another subscriber, and gets its own budget.
	other := boardRequest(adminUser, "")
	other.Header.Set("X-Client", "2001:db8:0:2::1")
	if err := h.board(httptest.NewRecorder(), other); err != nil {
		t.Fatalf("a different /64 was refused: %v", err)
	}
}

// The models board is cut to size before the page sorts it, so the cut has
// to be made by the metric the page shows.
func TestModelsBoardRanksByMetricBeforeTruncating(t *testing.T) {
	h, usageStore, db, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil
	ctx := context.Background()
	if err := h.settings.Set(ctx, settings.LeaderboardShowModels, "true"); err != nil {
		t.Fatal(err)
	}
	if err := h.settings.Set(ctx, settings.LeaderboardSize, "1"); err != nil {
		t.Fatal(err)
	}

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := provider.NewStore(db, box).Create(ctx, provider.CreateInput{
		Name: "Upstream", Kind: adapter.KindOpenAI, BaseURL: "https://api.example.com/v1",
		APIKey: "sk-test-key-1234", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	newModel := func(name string) string {
		record, err := h.models.Create(ctx, model.CreateInput{
			ProviderID: upstream.ID, ModelID: name, DisplayName: name, Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return record.ID
	}
	// heavy has the most tokens but the fewest distinct users; the other two
	// have more users and fewer tokens.
	heavy := newModel("heavy")
	wide := newModel("wide")
	narrow := newModel("narrow")

	users := user.NewStore(db)
	var members []string
	for i := 0; i < 5; i++ {
		member, err := users.Create(ctx, nil, user.CreateInput{
			Username: fmt.Sprintf("viewer%d", i), Nickname: "V", PasswordHash: "hash",
			Role: user.RoleUser, GroupID: adminUser.GroupID,
		})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, member.ID)
	}
	record := func(userID, modelID string, tokens int) {
		if err := usageStore.Write(ctx, usage.Record{
			UserID: userID, ModelID: modelID, InputTokens: tokens, TotalTokens: tokens,
			Status: usage.StatusOK, StartedAt: time.Now().Add(-time.Hour).UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	record(members[0], heavy, 9000)
	for _, member := range members[:4] {
		record(member, wide, 100)
	}
	for _, member := range members[:3] {
		record(member, narrow, 50)
	}

	w := httptest.NewRecorder()
	if err := h.board(w, boardRequest(adminUser, "10.0.0.1:1")); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].Name != "heavy" {
		t.Fatalf("a one-place models board showed %+v, want the model with the most tokens", got.Models)
	}
}

func boardRequest(account user.User, remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req.RemoteAddr = remoteAddr
	return req.WithContext(auth.WithUser(req.Context(), account))
}

// A board cached while the models setting was off has no model rows. Turning
// the setting on must not serve that board for the rest of the TTL.
func TestTurningTheModelsBoardOnIsNotHiddenByACachedBoard(t *testing.T) {
	h, usageStore, db, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil
	ctx := context.Background()
	if err := h.settings.Set(ctx, settings.LeaderboardShowModels, "false"); err != nil {
		t.Fatal(err)
	}

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := provider.NewStore(db, box).Create(ctx, provider.CreateInput{
		Name: "Upstream", Kind: adapter.KindOpenAI, BaseURL: "https://api.example.com/v1",
		APIKey: "sk-test-key-1234", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	record, err := h.models.Create(ctx, model.CreateInput{
		ProviderID: upstream.ID, ModelID: "fresh", DisplayName: "fresh", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := usageStore.Write(ctx, usage.Record{
		UserID: adminUser.ID, ModelID: record.ID, InputTokens: 10, TotalTokens: 10,
		Status: usage.StatusOK, StartedAt: time.Now().Add(-time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	// The first request builds and caches the board with the models board off.
	if err := h.board(httptest.NewRecorder(), boardRequest(adminUser, "10.0.0.1:1")); err != nil {
		t.Fatal(err)
	}

	if err := h.settings.Set(ctx, settings.LeaderboardShowModels, "true"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := h.board(w, boardRequest(adminUser, "10.0.0.1:1")); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].Name != "fresh" {
		t.Fatalf("models board after switching on showed %+v, want the one model with usage", got.Models)
	}
}
