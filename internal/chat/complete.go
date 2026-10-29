package chat

import (
	"context"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// Complete serves callers that own their transcript, such as the backoffice
// assistant. Prepare must have succeeded and its release must still be held:
// a tool continuation spends the same allowance as an ordinary chat turn.
func (s *Service) Complete(ctx context.Context, req TurnRequest, resolved model.Resolved, input adapter.ChatRequest, sink adapter.Sink) (adapter.Result, error) {
	input.Model = resolved.Upstream.Spec()
	input.Extra = resolved.Upstream.Extra()
	input.MaxTokens = resolved.Upstream.MaxOutputTokens
	if input.MaxTokens == 0 {
		input.MaxTokens = resolved.Model.MaxOutputTokens
	}
	input.Stream = req.Stream
	input.Reasoning = req.Reasoning
	if !resolved.Model.SupportsReasoning || !resolved.Upstream.SupportsReasoning {
		input.Reasoning = adapter.Reasoning{}
	} else if input.Reasoning.Enabled {
		input.Reasoning.Effort, input.Reasoning.Budget = resolved.Model.ResolveTier(input.Reasoning.Effort)
	}
	standing := s.systemPrompt(ctx, req, resolved, prepared{})
	if standing != "" {
		input.System = standing + "\n\n" + input.System
	}
	f := finished{requestID: id.New(), request: req, resolved: resolved, startedAt: time.Now()}
	result, err := s.registry.Chat(ctx, resolved.Provider, input, func(event adapter.Event) error {
		if event.Type == adapter.EventUsage {
			f.usage = f.usage.Merge(event.Usage)
		}
		if sink != nil {
			return sink(event)
		}
		return nil
	})
	f.usage = f.usage.Merge(result.Usage)
	status, code := StatusOK, ""
	if ctx.Err() != nil {
		status, code = StatusAborted, "cancelled"
	} else if err != nil {
		status = StatusError
		code, _ = Describe(err)
	}
	// A disconnected browser has still spent these tokens. Accounting must
	// finish before the caller gives its reservation back.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), SaveBudget)
	defer cancel()
	s.record(saveCtx, f, "", status, code)
	return result, err
}
