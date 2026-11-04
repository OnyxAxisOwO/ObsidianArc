package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Two models level on tokens share a place on the models board, whatever their
// user counts, as two accounts level on tokens share one on the accounts board.
// The user count only orders the models that are level.
func TestModelsLevelOnTokensShareAPlace(t *testing.T) {
	h, usageStore, db, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil
	ctx := context.Background()
	if err := h.settings.Set(ctx, settings.LeaderboardShowModels, "true"); err != nil {
		t.Fatal(err)
	}
	if err := h.settings.Set(ctx, settings.LeaderboardSize, "5"); err != nil {
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
	// Both models total 400 tokens. Wide has four users, narrow has two.
	wide := newModel("wide")
	narrow := newModel("narrow")

	users := user.NewStore(db)
	var members []string
	for i := 0; i < 4; i++ {
		member, err := users.Create(ctx, nil, user.CreateInput{
			Username: fmt.Sprintf("tie%d", i), Nickname: "T", PasswordHash: "hash",
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
	for _, member := range members {
		record(member, wide, 100)
	}
	for _, member := range members[:2] {
		record(member, narrow, 200)
	}

	w := httptest.NewRecorder()
	if err := h.board(w, boardRequest(adminUser, "10.0.0.2:1")); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []struct {
			Rank  int    `json:"rank"`
			Name  string `json:"name"`
			Value int64  `json:"value"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("the models board showed %+v, want both level models", got.Models)
	}
	for _, m := range got.Models {
		if m.Value != 400 {
			t.Fatalf("model %q has %d tokens, want 400", m.Name, m.Value)
		}
		if m.Rank != 1 {
			t.Errorf("model %q is place %d, want place 1: level on tokens, they share a place", m.Name, m.Rank)
		}
	}
	if got.Models[0].Name != "wide" {
		t.Errorf("the level models are ordered %q first, want the one with more users", got.Models[0].Name)
	}
}
