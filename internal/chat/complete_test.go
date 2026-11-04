package chat

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

func TestCompleteAccountsForAToolTurnWithoutWritingAnOrdinaryConversation(t *testing.T) {
	f := newFixture(t)
	f.upstream.fail(200, `{"choices":[{"message":{"content":"Inspecting","tool_calls":[{"id":"call-1","type":"function","function":{"name":"inspect","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`)
	var records []TurnRecord
	f.service.OnTurn = func(ctx context.Context, record TurnRecord) {
		if ctx.Err() != nil {
			t.Error("accounting inherited cancellation")
		}
		records = append(records, record)
	}
	req := TurnRequest{User: f.account, ModelID: f.model.ID}
	resolved, release, err := f.service.Prepare(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result, err := f.service.Complete(context.Background(), req, resolved, adapter.ChatRequest{
		System: "Management instructions", Messages: []adapter.Message{{Role: adapter.RoleUser, Parts: []adapter.Part{{Kind: adapter.PartText, Text: "Inspect"}}}},
		Tools: []adapter.Tool{{Name: "inspect"}},
	}, nil)
	if err != nil || len(result.ToolCalls) != 1 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if len(records) != 1 || records[0].Status != StatusOK || records[0].Usage.InputTokens != 12 || records[0].Usage.OutputTokens != 4 || records[0].RequestID == "" {
		t.Fatalf("accounting=%+v", records)
	}
	var count int
	if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM conversations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("ordinary conversations=%d error=%v", count, err)
	}
}

func TestCompleteStillAccountsForTokensWhenTheBrowserStops(t *testing.T) {
	f := newFixture(t)
	f.upstream.blockAfterFirstFrame(`{"choices":[{"delta":{"content":"Partial answer"}}]}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var record TurnRecord
	f.service.OnTurn = func(ctx context.Context, got TurnRecord) {
		if ctx.Err() != nil {
			t.Error("accounting was cancelled")
		}
		record = got
	}
	req := TurnRequest{User: f.account, ModelID: f.model.ID, Stream: true}
	resolved, release, err := f.service.Prepare(ctx, &req)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	f.service.Complete(ctx, req, resolved, adapter.ChatRequest{Messages: []adapter.Message{{Role: adapter.RoleUser, Parts: []adapter.Part{{Kind: adapter.PartText, Text: "Inspect"}}}}}, func(event adapter.Event) error {
		if event.Type == adapter.EventDelta {
			cancel()
			return context.Canceled
		}
		return nil
	})
	if record.Status != StatusAborted || record.Usage.Total() == 0 {
		t.Fatalf("stopped turn was not accounted for: %+v", record)
	}
}
