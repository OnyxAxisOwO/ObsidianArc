package conversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The code is written beside the sentence and comes back with it on every read
// path. A reader's screen translates the code, so a row that dropped it would
// show the English sentence again each time the conversation was reopened.
func TestErrorCodeRoundTripsWithItsSentence(t *testing.T) {
	ctx := context.Background()
	store, _, owner := attachmentFixture(t)
	thread, err := store.Create(ctx, nil, owner.ID, NewConversation{Title: "failures"})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	const sentence = "The provider is rate limiting this server. Try again shortly."
	failed, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID,
		UserID:         owner.ID,
		Role:           RoleAssistant,
		Error:          sentence,
		ErrorCode:      "provider_rate_limited",
	})
	if err != nil {
		t.Fatalf("append failure: %v", err)
	}
	if failed.ErrorCode != "provider_rate_limited" {
		t.Errorf("Append returned ErrorCode %q, want provider_rate_limited", failed.ErrorCode)
	}

	// A row written without a code keeps its sentence and has no code, which is
	// how rows from before the column existed read back.
	if _, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID,
		UserID:         owner.ID,
		Role:           RoleAssistant,
		Error:          "an old sentence",
	}); err != nil {
		t.Fatalf("append legacy-shaped failure: %v", err)
	}

	check := func(name string, got Message) {
		t.Helper()
		if got.ErrorCode != "provider_rate_limited" || got.Error != sentence {
			t.Errorf("%s: error = %q code = %q, want the sentence and its code", name, got.Error, got.ErrorCode)
		}
	}

	messages, err := store.Messages(ctx, nil, owner.ID, thread.ID)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	check("Messages", messages[0])
	if messages[1].ErrorCode != "" || messages[1].Error != "an old sentence" {
		t.Errorf("plain row = %q code %q, want its sentence and no code", messages[1].Error, messages[1].ErrorCode)
	}

	one, err := store.Message(ctx, nil, owner.ID, thread.ID, failed.ID)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	check("Message", one)

	page, err := store.MessagePage(ctx, nil, owner.ID, thread.ID, 0, 10)
	if err != nil {
		t.Fatalf("message page: %v", err)
	}
	check("MessagePage", page[0])

	// The conversation view encodes these values directly, so the code is in
	// the JSON the screen reads.
	encoded, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"error_code":"provider_rate_limited"`) {
		t.Errorf("encoded message lacks error_code: %s", encoded)
	}
}
