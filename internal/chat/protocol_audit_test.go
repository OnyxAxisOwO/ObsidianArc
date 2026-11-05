package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

func TestReasoningOnlyNonStreamingReplyIsPersistedAndAccounted(t *testing.T) {
	f := newFixture(t)
	off := false
	if _, err := f.models.Update(context.Background(), f.model.ID, model.Update{SupportsStreaming: &off}); err != nil {
		t.Fatal(err)
	}
	f.upstream.reply(`{"choices":[{"message":{"content":"","reasoning_content":"Only reasoning."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"completion_tokens_details":{"reasoning_tokens":20}}}`)
	var records []TurnRecord
	f.service.OnTurn = func(_ context.Context, r TurnRecord) { records = append(records, r) }
	out, err := f.turn(t, context.Background(), TurnRequest{Content: "think"})
	if err != nil || out.done == nil || out.failure != nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	messages := f.messages(t, out.start.ConversationID)
	if len(messages) != 2 || messages[1].Content != "" || messages[1].Reasoning != "Only reasoning." {
		t.Fatalf("messages=%+v", messages)
	}
	if len(records) != 1 || records[0].Status != StatusOK || records[0].Usage.Total() != 30 || records[0].Usage.OutputTokens != 0 || records[0].Usage.ReasoningTokens != 20 {
		t.Fatalf("records=%+v", records)
	}
}

func TestRefusalIsPreservedWithItsOwnOutcomeInBrowserChat(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic"} {
		for _, streaming := range []bool{false, true} {
			f := newFixture(t)
			if _, err := f.db.Exec(context.Background(), `UPDATE providers SET kind = ? WHERE id = ?`, kind, f.model.ProviderID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.models.Update(context.Background(), f.model.ID, model.Update{SupportsStreaming: &streaming}); err != nil {
				t.Fatal(err)
			}
			if kind == "openai" {
				if streaming {
					f.upstream.script(`{"choices":[{"delta":{"refusal":"Cannot comply."}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
				} else {
					f.upstream.reply(`{"choices":[{"message":{"content":null,"refusal":"Cannot comply."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
				}
			} else {
				if streaming {
					f.upstream.script(`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Cannot comply."}}`, `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":3}}`, `{"type":"message_stop"}`)
				} else {
					f.upstream.reply(`{"content":[{"type":"text","text":"Cannot comply."}],"stop_reason":"refusal","usage":{"input_tokens":10,"output_tokens":3}}`)
				}
			}
			var records []TurnRecord
			f.service.OnTurn = func(_ context.Context, r TurnRecord) { records = append(records, r) }
			out, err := f.turn(t, context.Background(), TurnRequest{Content: "question"})
			if err != nil || out.done == nil || !out.done.Refused || out.failure != nil {
				t.Fatalf("%s/%v out=%+v err=%v", kind, streaming, out, err)
			}
			messages := f.messages(t, out.start.ConversationID)
			if len(messages) != 2 || messages[1].Content != "Cannot comply." || messages[1].ErrorCode != "refusal" || messages[1].Error != "" {
				t.Fatalf("%s/%v messages=%+v", kind, streaming, messages)
			}
			if len(records) != 1 || records[0].Status != StatusError || records[0].ErrorCode != "refusal" || records[0].Usage.Total() != 13 {
				t.Fatalf("records=%+v", records)
			}
		}
	}
}

func TestCutStreamKeepsPartialOutputWithoutSuccessInBrowserChat(t *testing.T) {
	f := newFixture(t)
	// Bypass script's successful-fixture terminator to exercise a real EOF.
	f.upstream.mu.Lock()
	f.upstream.frames = []string{`{"choices":[{"delta":{"content":"unfinished"}}]}`}
	f.upstream.mu.Unlock()
	var records []TurnRecord
	f.service.OnTurn = func(_ context.Context, r TurnRecord) { records = append(records, r) }
	out, err := f.turn(t, context.Background(), TurnRequest{Content: "question"})
	if err != nil || out.done != nil || out.failure == nil || out.failure.Code != "provider_error" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	messages := f.messages(t, out.start.ConversationID)
	if len(messages) != 2 || messages[1].Content != "unfinished" || messages[1].ErrorCode != "provider_error" || messages[1].Error == "" {
		t.Fatalf("messages=%+v", messages)
	}
	if len(records) != 1 || records[0].Status != StatusError || !records[0].Usage.Estimated || records[0].Usage.Total() == 0 {
		t.Fatalf("records=%+v", records)
	}
	f.upstream.script(`{"choices":[{"delta":{"content":"next answer"}}]}`)
	if _, err := f.turn(t, context.Background(), TurnRequest{ConversationID: out.start.ConversationID, Content: "next question"}); err != nil {
		t.Fatal(err)
	}
	sent, _ := json.Marshal(f.upstream.lastRequest()["messages"])
	if strings.Contains(string(sent), "unfinished") {
		t.Fatalf("partial failed answer polluted future context: %s", sent)
	}
}
