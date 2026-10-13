package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// A conversation is not bounded by the chat, so an export must not hold all of
// one in memory before it checks the import limits. The messages are read a
// page at a time and the limit is checked as each page arrives, so a
// conversation past the limit is refused at the page that crosses it. What is
// held is bounded by the limit plus one page, whatever the conversation holds.
func TestAConversationPastTheMessageLimitIsRefusedWithoutBeingLoaded(t *testing.T) {
	f := newFixture(t)
	f.write(t, f.account, "Huge", "one", "two")

	const pageSize = 100
	reads := 0
	stream := &exportStream{
		service:         f.service,
		account:         f.account,
		pageSize:        100,
		messagePageSize: pageSize,
		envelope: mustEncodeEnvelope(t, Document{
			Format: Format, ExportedAt: 1, Username: f.account.Username,
		}),
		pageMessages: func(_ context.Context, _, _ string, afterSeq, limit int) ([]conversation.Message, error) {
			reads++
			page := make([]conversation.Message, limit)
			for i := range page {
				page[i] = conversation.Message{
					ID: "m", Seq: afterSeq + i + 1, Role: conversation.RoleUser, Content: "x",
				}
			}
			return page, nil
		},
	}

	var sink sinkWriter
	err := stream.writeTo(context.Background(), &sink)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a conversation with unbounded messages gave %v, want ErrTooLarge", err)
	}
	if limitPages := MaxMessagesPerImport/pageSize + 2; reads > limitPages {
		t.Fatalf("%d message pages read before refusing, want at most %d", reads, limitPages)
	}
}

type sinkWriter struct{ n int }

func (s *sinkWriter) Write(p []byte) (int, error) {
	s.n += len(p)
	return len(p), nil
}

func mustEncodeEnvelope(t *testing.T, document Document) []byte {
	t.Helper()
	envelope, err := encodeEnvelope(document)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

// Paging must not change a byte of the file. Each message page is written as it
// is read, so the separators between pages and the closing of the array are the
// places a streamed file can drift from json.Marshal. Four messages in pages of
// two end exactly on a page boundary, which is the case that reads one page
// more to find the end.
func TestPagedMessagesAreWhatMarshalWouldWrite(t *testing.T) {
	previous := exportMessagePageSize
	exportMessagePageSize = 2
	t.Cleanup(func() { exportMessagePageSize = previous })

	for _, count := range []int{0, 1, 2, 3, 4, 5} {
		f := newFixture(t)
		texts := make([]string, count)
		for i := range texts {
			texts[i] = "message " + string(rune('a'+i))
		}
		f.write(t, f.account, "Paged", texts...)

		encoded, document := f.export(t, f.account)
		want, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded, want) {
			t.Errorf("%d messages in pages of 2: streamed\n%s\nmarshal\n%s", count, encoded, want)
		}
		if got := len(document.Conversations[0].Messages); got != count {
			t.Errorf("%d messages written, want %d", got, count)
		}
	}
}
