package chat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordTurns collects what the ledger would be handed.
func recordTurns(f *fixture) func() []TurnRecord {
	var (
		mu      sync.Mutex
		records []TurnRecord
	)
	f.service.OnTurn = func(_ context.Context, record TurnRecord) {
		mu.Lock()
		records = append(records, record)
		mu.Unlock()
	}
	return func() []TurnRecord {
		mu.Lock()
		defer mu.Unlock()
		return append([]TurnRecord(nil), records...)
	}
}

// Stopping an answer just before the end used to bill nothing, because an
// OpenAI-style provider sends its count with the last frame and a stopped
// call was never estimated.
func TestAStoppedAnswerIsBilledForWhatItWrote(t *testing.T) {
	f := newFixture(t)
	records := recordTurns(f)

	long := strings.Repeat("word ", 400) // 2000 bytes, about 500 tokens
	release := f.upstream.blockAfterFirstFrame(
		textFrame(long),
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":600}}`,
	)
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	if _, err := f.turn(t, ctx, TurnRequest{Content: "write something long"}); err != nil {
		t.Fatalf("a stopped turn should not be an error: %v", err)
	}

	got := records()
	if len(got) != 1 || got[0].Status != StatusAborted {
		t.Fatalf("records = %+v", got)
	}
	if got[0].Usage.OutputTokens < 400 || got[0].Usage.InputTokens == 0 || !got[0].Usage.Estimated {
		t.Errorf("a stopped answer of ~500 tokens was billed as %+v", got[0].Usage)
	}
}

// Deleting the conversation while its answer streamed made the save fail,
// and the turn returned before it was recorded — no ledger row, no settle,
// and the reservation refunded: a free generation.
func TestDeletingTheConversationMidAnswerStillRecordsTheTurn(t *testing.T) {
	f := newFixture(t)
	records := recordTurns(f)

	release := f.upstream.blockAfterFirstFrame(
		textFrame("the beginning "),
		textFrame("and the rest"),
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":2000}}`,
	)

	req := TurnRequest{User: f.account, ModelID: f.model.ID, Content: "write a lot", Stream: true}
	resolved, done, err := f.service.Prepare(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	defer done()

	started := make(chan string, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- f.service.Run(context.Background(), req, resolved, func(event string, payload any) error {
			if start, ok := payload.(StartPayload); ok && event == EventStart {
				started <- start.ConversationID
			}
			return nil
		})
	}()

	conversationID := <-started
	time.Sleep(100 * time.Millisecond)
	if err := f.conversations.Delete(context.Background(), f.account.ID, conversationID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	close(release)
	<-finished

	got := records()
	if len(got) != 1 {
		t.Fatalf("the turn was not recorded: %+v", got)
	}
	if got[0].Usage.OutputTokens != 2000 {
		t.Errorf("recorded usage = %+v, want the provider's 2000 output tokens", got[0].Usage)
	}
}

// A work turn is several provider calls. Merging their reports made each
// replace the one before, so only the last call was billed.
func TestAWorkTurnIsBilledForEveryRound(t *testing.T) {
	f := newFixture(t)
	f.service.Tools = &stubBroker{tools: oneTool()}
	records := recordTurns(f)

	f.upstream.rounds = [][]string{
		{callFrame("c1", "user_list", `{}`),
			`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":1000,"completion_tokens":300}}`},
		{callFrame("c2", "user_list", `{}`),
			`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":1400,"completion_tokens":300}}`},
		{textFrame("done"),
			`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":1800,"completion_tokens":10}}`},
	}
	if _, _, err := f.workTurn(t, TurnRequest{Content: "look twice"}); err != nil {
		t.Fatal(err)
	}

	got := records()
	if len(got) != 1 {
		t.Fatalf("records = %+v", got)
	}
	if got[0].Usage.InputTokens != 4200 || got[0].Usage.OutputTokens != 610 {
		t.Errorf("usage = %+v, want 4200 in / 610 out across three rounds", got[0].Usage)
	}
}
