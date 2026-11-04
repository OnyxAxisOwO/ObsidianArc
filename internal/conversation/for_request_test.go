package conversation

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

// The tail a turn re-sends: the newest usable messages, oldest first, with
// failed assistant turns left out before the limit is counted — the same
// answer the gateway used to compute in Go from the whole transcript, now
// asked of the index instead.
func TestForRequestIsTheNewestUsableMessagesInOrder(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()
	thread, err := store.Create(ctx, nil, account.ID, NewConversation{Title: "Long"})
	if err != nil {
		t.Fatal(err)
	}

	picture, err := store.Upload(ctx, UploadInput{UserID: account.ID, Mime: "image/png", Data: bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	add := func(in AppendInput) {
		t.Helper()
		in.ConversationID, in.UserID = thread.ID, account.ID
		if _, err := store.Append(ctx, nil, in); err != nil {
			t.Fatal(err)
		}
	}
	for turn := 1; turn <= 6; turn++ {
		question := AppendInput{Role: RoleUser, Content: fmt.Sprintf("q%d", turn)}
		if turn == 6 {
			question.AttachmentIDs = []string{picture.ID}
		}
		add(question)
		switch turn {
		case 4:
			// A failed turn, and an empty one: neither may reach the model,
			// and neither may use up a place in the limit.
			add(AppendInput{Role: RoleAssistant, Error: "upstream unavailable"})
		case 5:
			add(AppendInput{Role: RoleAssistant, Content: ""})
		default:
			add(AppendInput{Role: RoleAssistant, Content: fmt.Sprintf("a%d", turn)})
		}
	}

	got, err := store.ForRequest(ctx, nil, account.ID, thread.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	var contents []string
	for _, message := range got {
		contents = append(contents, message.Content)
	}
	want := []string{"a3", "q4", "q5", "q6", "a6"}
	if fmt.Sprint(contents) != fmt.Sprint(want) {
		t.Fatalf("for request = %v, want %v", contents, want)
	}
	if len(got[3].Attachments) != 1 || got[3].Attachments[0].ID != picture.ID {
		t.Fatalf("the picture did not come with its message: %+v", got[3].Attachments)
	}

	// No limit is the whole usable transcript, which is what Messages minus
	// the failed turns always was.
	all, err := store.ForRequest(ctx, nil, account.ID, thread.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 10 {
		t.Fatalf("unlimited = %d messages, want 10", len(all))
	}

	// Another account's id reads nothing, the rule every read here keeps.
	other, err := store.ForRequest(ctx, nil, "someone-else", thread.ID, 5)
	if err != nil || len(other) != 0 {
		t.Fatalf("another account read %d messages (err %v)", len(other), err)
	}
}

// The reasoning a model showed is not sent back to it, so a turn does not read
// it: it is the widest column a message has, and a turn replays forty of them.
// The transcript a reader opens still carries it.
func TestForRequestLeavesTheReasoningBehind(t *testing.T) {
	store, _, account := attachmentFixture(t)
	ctx := context.Background()
	thread, err := store.Create(ctx, nil, account.ID, NewConversation{Title: "Thinking"})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []AppendInput{
		{Role: RoleUser, Content: "why"},
		{Role: RoleAssistant, Content: "because", Reasoning: "a long chain of thought", ModelName: "m", Stats: &Stats{}},
	} {
		in.ConversationID, in.UserID = thread.ID, account.ID
		if _, err := store.Append(ctx, nil, in); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ForRequest(ctx, nil, account.ID, thread.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Content != "because" || got[1].Role != RoleAssistant || got[1].ID == "" {
		t.Fatalf("for request = %+v", got)
	}
	if got[1].Reasoning != "" || got[1].Stats != nil || got[1].ModelName != "" {
		t.Fatalf("a turn read what it never sends: %+v", got[1])
	}

	transcript, err := store.Messages(ctx, nil, account.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if transcript[1].Reasoning != "a long chain of thought" {
		t.Fatalf("the transcript lost its reasoning: %q", transcript[1].Reasoning)
	}
}
