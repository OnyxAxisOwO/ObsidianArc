package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// exportPageSize is how many conversation rows one read of an export takes.
// An export holds one such page of rows, and one page of messages (see
// exportMessagePageSize), at a time. A variable so a test can span several
// pages without writing a hundred conversations.
var exportPageSize = 100

// exportMessagePageSize is how many messages of one conversation one read of an
// export takes. As with exportPageSize, a variable so a test can span pages.
var exportMessagePageSize = 100

// exportStream is an export that has been measured and not yet written. Every
// refusal an export can make is decided in openExport, before the response has
// started: after the first byte the status is 200 and cannot be taken back.
type exportStream struct {
	service  *Service
	account  user.User
	pageSize int
	envelope []byte
	// Reads one page of a conversation's messages. Set by openExport; a field so
	// a test can stand in a conversation of any length.
	pageMessages    messagePager
	messagePageSize int
}

// messagePager reads up to limit messages of one conversation, oldest first,
// after sequence number afterSeq.
type messagePager func(ctx context.Context, userID, conversationID string, afterSeq, limit int) ([]conversation.Message, error)

// openExport measures the whole export before any of it is sent. The envelope
// is built here because it carries the preferences and the time the file is
// stamped with. The conversations are then written into a counter by writeTo
// itself, so the counter sees exactly the bytes a download would carry, and the
// walk stops at the first limit the import would refuse.
//
// That costs the account one more read than the download does, and the limits
// bound it: the walk stops as soon as one is crossed, so an account far past
// them is refused after about one import's worth of reads.
func (s *Service) openExport(ctx context.Context, account user.User) (*exportStream, error) {
	// Already a JSON document in the store, carried across as it is. Get answers
	// an account that never saved preferences with an empty object, so an error
	// here is a failed read. Refusing keeps the export from writing a file that
	// quietly lacks the preferences the README says an export carries.
	stored, err := s.preferences.Get(ctx, account.ID)
	if err != nil {
		return nil, fmt.Errorf("backup: read preferences: %w", err)
	}
	var preferences json.RawMessage
	if len(stored) > 0 {
		preferences = stored
	}

	envelope, err := encodeEnvelope(Document{
		Format:      Format,
		ExportedAt:  time.Now().UnixMilli(),
		Username:    account.Username,
		Preferences: preferences,
	})
	if err != nil {
		return nil, err
	}

	// At zero no page is ever short, so the walk would not end, and the cursor
	// would be taken from an empty page. Held at one at the least.
	stream := &exportStream{
		service:  s,
		account:  account,
		pageSize: max(exportPageSize, 1),
		envelope: envelope,
		pageMessages: func(ctx context.Context, userID, conversationID string, afterSeq, limit int) ([]conversation.Message, error) {
			return s.conversations.MessagePage(ctx, nil, userID, conversationID, afterSeq, limit)
		},
		messagePageSize: max(exportMessagePageSize, 1),
	}
	if err := stream.writeTo(ctx, &sizeLimit{}); err != nil {
		return nil, err
	}
	return stream, nil
}

// encodeEnvelope is the document up to where its conversations begin. It is
// marshalled through the Document's own tags, so the field names and the
// omitempty rules are the ones Import reads and cannot drift from them. The
// empty conversations field that marshalling adds at the end is cut, so the
// array can be written after it.
func encodeEnvelope(document Document) ([]byte, error) {
	const tail = `,"conversations":null}`
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("backup: encode envelope: %w", err)
	}
	if !bytes.HasSuffix(encoded, []byte(tail)) {
		// A field has been added after conversations. Writing the array where
		// that field is expected would produce a file Import misreads, so fail
		// here rather than guess at the layout.
		return nil, errors.New("backup: conversations must stay the last field of Document")
	}
	return encoded[:len(encoded)-len(tail)], nil
}

// writeTo streams the document to w one conversation at a time.
//
// The bytes are what json.Marshal of the same Document produces, so a file
// from this server reads back into Import exactly as one from the previous
// release did. The count limits are applied here too, so openExport's dry run
// and the download refuse the same accounts.
//
// Once w has been written to, a failure cannot be reported. An account that
// changes between the dry run and this pass, or a read that fails, leaves the
// body truncated where it stopped. That is not valid JSON, so a client that
// parses it fails, and Import refuses it: nothing is half-restored, but the
// file is not usable. The status cannot be changed to say so, and the log
// carries the cause.
func (e *exportStream) writeTo(ctx context.Context, w io.Writer) error {
	if _, err := w.Write(e.envelope); err != nil {
		return err
	}
	if _, err := io.WriteString(w, `,"conversations":[`); err != nil {
		return err
	}

	conversations, messages := 0, 0
	cursor := ""
	for {
		page, err := e.service.conversations.ListForExport(ctx, e.account.ID, cursor, e.pageSize)
		if err != nil {
			return fmt.Errorf("backup: list conversations: %w", err)
		}
		for _, record := range page {
			conversations++
			if overImportLimits(conversations, messages) {
				return ErrTooLarge
			}
			if conversations > 1 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			messages, err = e.writeThread(ctx, w, record, conversations, messages)
			if err != nil {
				return err
			}
		}

		// A short page is the last one. A full page is followed by another
		// read, which is what finds the end of an account whose conversation
		// count is an exact multiple of the page size.
		if len(page) < e.pageSize {
			break
		}
		cursor = page[len(page)-1].ID
	}

	_, err := io.WriteString(w, "]}")
	return err
}

// writeThread streams one conversation as the next entry of the document. Its
// messages are read a page at a time, and each page is checked against the
// import limits before it is written, so a conversation past them is refused at
// the page that crosses the limit. What is held in memory is bounded by the
// limit plus one page, not by the conversation. messages is the total across
// the threads before this one; the total including this one is returned.
//
// Every text field is scrubbed of NUL here rather than left for Import to
// refuse. A conversation can already hold one, because the chat path and
// renaming keep what they were given, and an export that carried it would be a
// file its own account could not restore.
func (e *exportStream) writeThread(ctx context.Context, w io.Writer, record conversation.Conversation, conversations, messages int) (int, error) {
	header, err := encodeThreadHeader(Thread{
		Title:     scrubNUL(record.Title),
		Pinned:    record.Pinned,
		CreatedAt: record.CreatedAt,
	})
	if err != nil {
		return messages, err
	}
	if _, err := w.Write(header); err != nil {
		return messages, err
	}

	written, afterSeq := 0, 0
	for {
		page, err := e.pageMessages(ctx, e.account.ID, record.ID, afterSeq, e.messagePageSize)
		if err != nil {
			return messages, fmt.Errorf("backup: read %s: %w", record.ID, err)
		}
		if overImportLimits(conversations, messages+len(page)) {
			return messages, ErrTooLarge
		}
		messages += len(page)

		for _, message := range page {
			encoded, err := json.Marshal(turnOf(message))
			if err != nil {
				return messages, fmt.Errorf("backup: encode %s: %w", record.ID, err)
			}
			if written > 0 {
				if _, err := io.WriteString(w, ","); err != nil {
					return messages, err
				}
			}
			if _, err := w.Write(encoded); err != nil {
				return messages, err
			}
			written++
		}

		if len(page) < e.messagePageSize {
			break
		}
		afterSeq = page[len(page)-1].Seq
	}

	_, err = io.WriteString(w, "]}")
	return messages, err
}

// encodeThreadHeader is a thread up to where its messages begin, marshalled
// through the Thread's own tags so the field names and omitempty rules cannot
// drift from Import's. The empty messages array that marshalling adds at the
// end is cut, so the messages can be written after it.
func encodeThreadHeader(thread Thread) ([]byte, error) {
	const tail = `"messages":[]}`
	thread.Messages = []Turn{}
	encoded, err := json.Marshal(thread)
	if err != nil {
		return nil, fmt.Errorf("backup: encode thread: %w", err)
	}
	if !bytes.HasSuffix(encoded, []byte(tail)) {
		// Same reasoning as encodeEnvelope: a field added after messages would
		// put the array somewhere Import does not expect it.
		return nil, errors.New("backup: messages must stay the last field of Thread")
	}
	return append(encoded[:len(encoded)-len(tail)], `"messages":[`...), nil
}

// turnOf is one stored message as the document carries it.
func turnOf(message conversation.Message) Turn {
	return Turn{
		Role:      scrubNUL(string(message.Role)),
		Content:   scrubNUL(message.Content),
		Reasoning: scrubNUL(message.Reasoning),
		Error:     scrubNUL(message.Error),
		ErrorCode: scrubNUL(message.ErrorCode),
		ModelName: scrubNUL(message.ModelName),
		CreatedAt: message.CreatedAt,
		Images:    len(message.Attachments),
	}
}

// sizeLimit counts an export's dry run and refuses as soon as the total is more
// than the importer reads. The import body is cut by a MaxBytesReader at
// MaxDocumentBytes (http.go), so a file of exactly that size is read and one
// byte more is not.
type sizeLimit struct {
	written int64
}

func (s *sizeLimit) Write(p []byte) (int, error) {
	s.written += int64(len(p))
	if s.written > MaxDocumentBytes {
		return 0, ErrTooLarge
	}
	return len(p), nil
}

// scrubNUL replaces U+0000 with U+FFFD. U+FFFD marks where something was rather
// than deleting it, so the restored text shows the character was there.
func scrubNUL(value string) string {
	if strings.IndexByte(value, 0) < 0 {
		return value
	}
	return strings.ReplaceAll(value, "\x00", "\uFFFD")
}
