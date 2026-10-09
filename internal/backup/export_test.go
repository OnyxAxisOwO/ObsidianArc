package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// export runs the export the way the endpoint does and returns the bytes a
// client would receive, with what they decode to. Asserting on the Document
// alone would miss a fault in the encoding.
func (f *fixture) export(t *testing.T, account user.User) ([]byte, Document) {
	t.Helper()
	ctx := context.Background()

	stream, err := f.service.openExport(ctx, account)
	if err != nil {
		t.Fatalf("open export: %v", err)
	}
	var buf bytes.Buffer
	if err := stream.writeTo(ctx, &buf); err != nil {
		t.Fatalf("write export: %v", err)
	}

	var document Document
	if err := json.Unmarshal(buf.Bytes(), &document); err != nil {
		t.Fatalf("the export is not a document: %v\n%s", err, buf.Bytes())
	}
	return buf.Bytes(), document
}

// withExportPageSize reads the export n conversations at a time until the test
// ends. Only the export reads it, and tests in this package do not run in
// parallel, so the change cannot leak into another test.
func withExportPageSize(t *testing.T, n int) {
	t.Helper()
	previous := exportPageSize
	exportPageSize = n
	t.Cleanup(func() { exportPageSize = previous })
}

// hookedWriter collects an export and runs do once, as soon as what has been
// written so far contains marker. It is how a test changes the account in the
// middle of an export.
type hookedWriter struct {
	bytes.Buffer
	marker string
	do     func()
	fired  bool
}

func (h *hookedWriter) Write(p []byte) (int, error) {
	n, err := h.Buffer.Write(p)
	if !h.fired && bytes.Contains(h.Buffer.Bytes(), []byte(h.marker)) {
		h.fired = true
		h.do()
	}
	return n, err
}

// The file is what json.Marshal writes for the same Document, byte for byte.
// Import reads it through the Document's tags, so a streamed file that differs
// from Marshal in spacing, escaping or field order is a different format.
func TestStreamedExportIsWhatMarshalWouldWrite(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	id := f.write(t, f.account, `Lamps <and> "quotes" & more`, "what is a lamp?")
	if _, err := f.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: id, UserID: f.account.ID, Role: conversation.RoleAssistant,
		Content: "a source of light < > & heat", Reasoning: "the question is about lamps",
		ModelName: "Test <Model>",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.preferences.Merge(ctx, f.account.ID, map[string]json.RawMessage{
		"accent": json.RawMessage(`"violet"`),
	}); err != nil {
		t.Fatal(err)
	}

	encoded, document := f.export(t, f.account)
	want, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Errorf("streamed export differs from json.Marshal of the same document\nstreamed: %s\nmarshal:  %s", encoded, want)
	}

	// An account with nothing in it. Decoding turns "conversations":null into
	// nil, which Marshal writes back as null, so the comparison alone would
	// pass a file that should carry an empty array. Check the array directly.
	empty, _ := f.export(t, f.stranger)
	if !bytes.Contains(empty, []byte(`"conversations":[]}`)) {
		t.Errorf("an empty account's export has no empty conversations array: %s", empty)
	}
}

// What a person exports and then imports is what they had: the title, the
// pinned flag, and every field a turn carries.
func TestExportedDocumentImportsBackUnchanged(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	id := f.write(t, f.account, "Lamps", "what is a lamp?")
	if _, err := f.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: id, UserID: f.account.ID, Role: conversation.RoleAssistant,
		Content: "a source of light", Reasoning: "lamps make light", ModelName: "Test Model",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: id, UserID: f.account.ID, Role: conversation.RoleAssistant,
		Error: "the provider timed out",
	}); err != nil {
		t.Fatal(err)
	}
	pinned := true
	if _, err := f.conversations.Update(ctx, f.account.ID, id, conversation.Update{Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}

	_, document := f.export(t, f.account)
	if _, err := f.service.Import(ctx, f.stranger, document); err != nil {
		t.Fatalf("import of the export: %v", err)
	}

	threads, err := f.conversations.List(ctx, f.stranger.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Title != "Lamps" || !threads[0].Pinned {
		t.Fatalf("restored %+v, want one pinned conversation titled Lamps", threads)
	}
	messages, err := f.conversations.Messages(ctx, nil, f.stranger.ID, threads[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("restored %d messages, want 3", len(messages))
	}
	answer := messages[1]
	if answer.Content != "a source of light" || answer.Reasoning != "lamps make light" || answer.ModelName != "Test Model" {
		t.Errorf("answer restored as %+v", answer)
	}
	if messages[2].Error != "the provider timed out" {
		t.Errorf("error restored as %q", messages[2].Error)
	}
}

// The export reads conversations a page at a time. Every conversation must
// arrive once, including when the account is an exact multiple of the page and
// the last read comes back empty.
func TestExportCarriesEveryConversationAcrossPages(t *testing.T) {
	for total := 0; total <= 5; total++ {
		t.Run(fmt.Sprintf("%d conversations", total), func(t *testing.T) {
			withExportPageSize(t, 2)
			f := newFixture(t)
			for i := 0; i < total; i++ {
				f.write(t, f.account, fmt.Sprintf("Thread %d", i), "hello")
			}

			_, document := f.export(t, f.account)
			if len(document.Conversations) != total {
				t.Fatalf("exported %d conversations, want %d", len(document.Conversations), total)
			}
			seen := map[string]int{}
			for _, thread := range document.Conversations {
				seen[thread.Title]++
			}
			for title, count := range seen {
				if count != 1 {
					t.Errorf("%q exported %d times", title, count)
				}
			}
		})
	}
}

// The export holds every conversation the account has. MaxConversations limits
// what one import file may carry; it is not a reason to drop conversations on
// the way out, which would leave the file silently short of the account.
func TestExportIsWholeAboveTheImportLimit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	total := MaxConversations + 1
	for i := 0; i < total; i++ {
		if _, err := f.conversations.Create(ctx, nil, f.account.ID,
			conversation.NewConversation{Title: fmt.Sprintf("t%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	_, document := f.export(t, f.account)
	if len(document.Conversations) != total {
		t.Fatalf("exported %d conversations, want all %d", len(document.Conversations), total)
	}
}

// The export reads the account while the account is still in use, and a turn
// in one conversation moves it to the top of the rail. Paging by the rail's
// order would carry the conversations not yet read across a page boundary
// behind the export, so some would be skipped and others repeated. The page is
// keyed on the ID, which a turn cannot change.
func TestAPageBoundaryHoldsWhenConversationsAreUsedMidExport(t *testing.T) {
	ctx := context.Background()
	withExportPageSize(t, 2)
	f := newFixture(t)

	var ids []string
	for i := 0; i < 4; i++ {
		ids = append(ids, f.write(t, f.account, fmt.Sprintf("Thread %d", i), "hello"))
		// ULIDs have millisecond resolution, so conversations created in the
		// same millisecond may not sort in creation order. The pause keeps
		// creation order, ID order and update order the same, which is the
		// order this test is written against.
		time.Sleep(2 * time.Millisecond)
	}

	stream, err := f.service.openExport(ctx, f.account)
	if err != nil {
		t.Fatal(err)
	}
	// Once the first conversation is written, every conversation not yet
	// written gets a turn. Whichever one the export reads first, the rest are
	// all moved to the top of the rail while the export is still under way.
	writer := &hookedWriter{marker: `"conversations":[{`}
	writer.do = func() {
		for i, id := range ids {
			if bytes.Contains(writer.Bytes(), []byte(fmt.Sprintf(`"title":"Thread %d"`, i))) {
				continue
			}
			if _, err := f.conversations.Append(ctx, nil, conversation.AppendInput{
				ConversationID: id, UserID: f.account.ID,
				Role: conversation.RoleUser, Content: "one more",
			}); err != nil {
				t.Error(err)
			}
		}
	}
	if err := stream.writeTo(ctx, writer); err != nil {
		t.Fatalf("write export: %v", err)
	}
	if !writer.fired {
		t.Fatal("the mid-export turns never happened, so the test proved nothing")
	}

	var document Document
	if err := json.Unmarshal(writer.Bytes(), &document); err != nil {
		t.Fatalf("the export is not a document: %v", err)
	}
	seen := map[string]int{}
	for _, thread := range document.Conversations {
		seen[thread.Title]++
	}
	for i := 0; i < 4; i++ {
		title := fmt.Sprintf("Thread %d", i)
		if seen[title] != 1 {
			t.Errorf("%q exported %d times, want once", title, seen[title])
		}
	}
}
