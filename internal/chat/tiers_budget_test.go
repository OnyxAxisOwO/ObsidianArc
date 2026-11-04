package chat

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

// The administrator's tier budgets are what the provider is asked to think
// with. The adapter's built-in budgets apply only when a model has no list,
// so this runs the Anthropic request end to end through Authorize, the read
// that used to drop the list before the turn was built.
func TestAnthropicThinkingBudgetComesFromTheConfiguredTier(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	anthropic, err := provider.NewStore(f.db, box).Create(ctx, provider.CreateInput{
		Name:    "Anthropic stub",
		Kind:    adapter.KindAnthropic,
		BaseURL: f.upstream.server.URL,
		APIKey:  "sk-test",
		Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	record, err := f.models.Create(ctx, model.CreateInput{
		ProviderID:  anthropic.ID,
		ModelID:     "claude-tiers",
		DisplayName: "Claude tiers",
		Enabled:     true,
		ReasoningTiers: []model.ReasoningTier{
			{ID: "low", Name: "轻", Budget: 2000},
			{ID: "high", Name: "深", Budget: 30000},
		},
		Capabilities: model.Capabilities{
			SupportsStreaming: true, SupportsSystemPrompt: true, SupportsReasoning: true,
		},
		Weights: model.Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.model = record

	// max_tokens is the budget plus the headroom the adapter keeps above it,
	// and never less than the adapter's own default ceiling of 4096.
	cases := []struct {
		effort    adapter.Effort
		budget    float64
		maxTokens float64
	}{
		{adapter.EffortLow, 2000, 4096},
		{adapter.EffortHigh, 30000, 31024},
	}
	for _, c := range cases {
		f.upstream.script(
			`{"type":"message_start","message":{"usage":{"input_tokens":3}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_stop"}`,
		)
		if _, err := f.turn(t, ctx, TurnRequest{
			Content:   "think",
			Reasoning: adapter.Reasoning{Enabled: true, Effort: c.effort},
		}); err != nil {
			t.Fatalf("%s turn: %v", c.effort, err)
		}

		sent := f.upstream.lastRequest()
		thinking, _ := sent["thinking"].(map[string]any)
		if thinking["budget_tokens"] != c.budget {
			t.Errorf("%s: thinking.budget_tokens = %v, want %v (the configured tier)",
				c.effort, thinking["budget_tokens"], c.budget)
		}
		if sent["max_tokens"] != c.maxTokens {
			t.Errorf("%s: max_tokens = %v, want %v", c.effort, sent["max_tokens"], c.maxTokens)
		}
	}
}
