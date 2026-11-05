package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

func TestRewindPreventsOldTurnAppendingToNewBranch(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "stopped"} {
		t.Run(outcome, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			req := TurnRequest{User: f.account, ModelID: f.model.ID, Content: "old question"}
			resolved, release, err := f.service.Prepare(ctx, &req)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			old, err := f.service.openTurn(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			replacement := TurnRequest{User: f.account, ModelID: f.model.ID, ConversationID: old.conversationID, TruncateFromMessageID: old.userMessageID, Content: "new question"}
			if _, err := f.service.openTurn(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			var records []TurnRecord
			f.service.OnTurn = func(_ context.Context, r TurnRecord) { records = append(records, r) }
			done := finished{request: req, resolved: resolved, prepared: old, answer: "old answer", startedAt: time.Now()}
			emit := func(string, any) error { return nil }
			switch outcome {
			case "success":
				err = f.service.finishOK(ctx, done, emit)
			case "failure":
				err = f.service.finishFailed(ctx, ctx, done, errors.New("upstream"), emit, false)
			case "stopped":
				err = f.service.finishFailed(ctx, ctx, done, context.Canceled, emit, false)
			}
			if !errors.Is(err, conversation.ErrBranchChanged) {
				t.Fatalf("finish error = %v, want branch changed", err)
			}
			messages := f.messages(t, old.conversationID)
			if len(messages) != 1 || messages[0].Content != "new question" {
				t.Fatalf("new branch = %+v", messages)
			}
			if len(records) != 1 || records[0].MessageID != "" {
				t.Fatalf("usage records = %+v", records)
			}
		})
	}
}

func TestEditingMessageInvalidatesAnInFlightReply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := TurnRequest{User: f.account, ModelID: f.model.ID, Content: "before"}
	resolved, release, err := f.service.Prepare(ctx, &req)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	old, err := f.service.openTurn(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conversations.UpdateMessage(ctx, nil, f.account.ID, old.conversationID, old.userMessageID, "after"); err != nil {
		t.Fatal(err)
	}
	err = f.service.finishOK(ctx, finished{request: req, resolved: resolved, prepared: old, answer: "obsolete", startedAt: time.Now()}, func(string, any) error { return nil })
	if !errors.Is(err, conversation.ErrBranchChanged) {
		t.Fatalf("finish error = %v", err)
	}
	messages := f.messages(t, old.conversationID)
	if len(messages) != 1 || messages[0].Content != "after" {
		t.Fatalf("messages = %+v", messages)
	}
}
