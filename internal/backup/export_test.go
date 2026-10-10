package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
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

// bulk writes conversations of messagesEach messages into an account. Each
// conversation's messages go in as one INSERT ... SELECT rather than an Append
// apiece: Append is several statements per message, and a fixture of fifty
// thousand messages built that way would take longer than the import it is
// there to test. Nothing under test reads the conversation's own message count,
// which is the one thing this skips maintaining.
func (f *fixture) bulk(t *testing.T, owner user.User, conversations, messagesEach int, content string) {
	t.Helper()
	ctx := context.Background()

	err := f.service.db.Tx(ctx, func(tx *database.Tx) error {
		for c := 0; c < conversations; c++ {
			created, err := f.conversations.Create(ctx, tx, owner.ID,
				conversation.NewConversation{Title: fmt.Sprintf("Bulk %d", c)})
			if err != nil {
				return err
			}
			if messagesEach == 0 {
				continue
			}
			if _, err := tx.Exec(ctx, `WITH RECURSIVE numbers(n) AS (
					SELECT 1 UNION ALL SELECT n + 1 FROM numbers WHERE n < ?)
				INSERT INTO messages (id, conversation_id, user_id, seq, role, content, created_at)
				SELECT ? || '-' || n, ?, ?, n, 'user', ?, 0 FROM numbers`,
				messagesEach, created.ID, created.ID, owner.ID, content); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The export and the import answer "is this too large" with the same figures.
// Under each limit an export is written and reads back into another account;
// one step past it the export is refused, because the file it would write is
// one Import turns away. Before this, the export carried everything and the
// import refused the result.
func TestExportRoundTripsUnderEachLimitAndIsRefusedPastIt(t *testing.T) {
	ctx := context.Background()

	t.Run("conversations", func(t *testing.T) {
		f := newFixture(t)
		f.bulk(t, f.account, MaxConversations, 1, "hello")

		_, document := f.export(t, f.account)
		result, err := f.service.Import(ctx, f.stranger, document)
		if err != nil {
			t.Fatalf("import of %d conversations: %v", MaxConversations, err)
		}
		if result.Conversations != MaxConversations {
			t.Fatalf("restored %d conversations, want %d", result.Conversations, MaxConversations)
		}

		f.write(t, f.account, "One too many", "hello")
		if _, err := f.service.openExport(ctx, f.account); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("export of %d conversations gave %v, want ErrTooLarge", MaxConversations+1, err)
		}
	})

	t.Run("messages", func(t *testing.T) {
		f := newFixture(t)
		const perConversation = 1000
		f.bulk(t, f.account, MaxMessagesPerImport/perConversation, perConversation, "hello")

		_, document := f.export(t, f.account)
		result, err := f.service.Import(ctx, f.stranger, document)
		if err != nil {
			t.Fatalf("import of %d messages: %v", MaxMessagesPerImport, err)
		}
		if result.Messages != MaxMessagesPerImport {
			t.Fatalf("restored %d messages, want %d", result.Messages, MaxMessagesPerImport)
		}

		f.write(t, f.account, "One too many", "hello")
		if _, err := f.service.openExport(ctx, f.account); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("export of %d messages gave %v, want ErrTooLarge", MaxMessagesPerImport+1, err)
		}
	})

	// The byte limit is not reached exactly here: messages are capped at
	// 32000 characters, so the size comes in steps. The exact figure is held by
	// TestSizeLimitRefusesOnlyPastTheImportBodyLimit. This case shows that a
	// document well under the limit reads back, and one well over it is refused.
	t.Run("bytes", func(t *testing.T) {
		f := newFixture(t)
		text := strings.Repeat("x", conversation.MaxContentChars)
		f.bulk(t, f.account, 1, 500, text)

		_, document := f.export(t, f.account)
		if _, err := f.service.Import(ctx, f.stranger, document); err != nil {
			t.Fatalf("import of about 16 MiB: %v", err)
		}

		f.bulk(t, f.account, 1, 600, text)
		if _, err := f.service.openExport(ctx, f.account); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("export of about 35 MiB gave %v, want ErrTooLarge", err)
		}
	})
}

// The importer reads a body of exactly MaxDocumentBytes and refuses one byte
// more, so the dry run must allow that much and not a byte beyond it.
func TestSizeLimitRefusesOnlyPastTheImportBodyLimit(t *testing.T) {
	var counter sizeLimit
	chunk := make([]byte, 1<<16)
	for written := 0; written < MaxDocumentBytes; written += len(chunk) {
		if _, err := counter.Write(chunk); err != nil {
			t.Fatalf("refused at %d bytes, inside the limit: %v", written, err)
		}
	}
	if _, err := counter.Write([]byte{'}'}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one byte past the limit gave %v, want ErrTooLarge", err)
	}
}

// Text can already hold a NUL: the chat path and renaming keep what they were
// given, and SQLite keeps the byte. Import refuses one, so an export that wrote
// it would be a file its own account could not restore. The export writes the
// replacement character where the NUL was.
func TestExportOfStoredNULImportsBack(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	id := f.write(t, f.account, "Lamp\x00s", "what is a lamp\x00?")
	if _, err := f.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: id, UserID: f.account.ID, Role: conversation.RoleAssistant,
		Content: "a source of light", Reasoning: "lamps\x00 make light",
		Error: "the provider\x00 timed out", ModelName: "Test\x00Model",
	}); err != nil {
		t.Fatal(err)
	}

	encoded, document := f.export(t, f.account)
	if bytes.IndexByte(encoded, 0) >= 0 || bytes.Contains(encoded, []byte(`\u0000`)) {
		t.Fatalf("the export still carries a NUL: %s", encoded)
	}
	if _, err := f.service.Import(ctx, f.stranger, document); err != nil {
		t.Fatalf("import of an export that held a stored NUL: %v", err)
	}

	threads, err := f.conversations.List(ctx, f.stranger.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Title != "Lamp\uFFFDs" {
		t.Fatalf("restored %+v, want one conversation titled Lamp\\uFFFDs", threads)
	}
	messages, err := f.conversations.Messages(ctx, nil, f.stranger.ID, threads[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("restored %d messages, want 2", len(messages))
	}
	if messages[0].Content != "what is a lamp\uFFFD?" {
		t.Errorf("question restored as %q", messages[0].Content)
	}
	answer := messages[1]
	if answer.Reasoning != "lamps\uFFFD make light" || answer.Error != "the provider\uFFFD timed out" ||
		answer.ModelName != "Test\uFFFDModel" {
		t.Errorf("answer restored as %+v", answer)
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
