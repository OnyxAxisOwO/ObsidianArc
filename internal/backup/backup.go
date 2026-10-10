// Package backup is how an account takes its data out and puts it back.
//
// It exists because an account's conversations and preferences are the user's,
// not the instance's, and a self-hosted server that cannot hand them back is
// asking to be trusted rather than earning it. One JSON document holds both:
// small enough to read in an editor, plain enough to be worth keeping.
//
// Two things it deliberately is not.
//
// It is not a backup of the server. There is nothing here about providers,
// models, keys or other accounts — an operator's backup is a copy of the
// database, and this is a person's copy of their own conversations.
//
// It is not a restore. Importing adds conversations alongside whatever is
// already there rather than replacing anything, because a merge that silently
// deleted the history you were trying to protect would be the worst possible
// failure of a feature called import.
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
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Format is the shape of the document. Bumped only when an older file would
// be read wrongly rather than merely incompletely.
const Format = 1

// Bounds on what an import may carry. An account can already create this much
// by hand; the point is that one request cannot.
//
// An export refuses an account past the same figures, so every file this server
// writes is one its own import reads back. Both sides compare against these
// constants rather than keeping a copy each.
const (
	MaxDocumentBytes     = 32 << 20
	MaxConversations     = 2000
	MaxMessagesPerImport = 50000
	// What one account may be storing in total.
	//
	// The three limits above bound one request. None of them bounds the
	// account: importing writes new conversations rather than replacing what
	// is there, so the same document sent twenty times is twenty copies, and
	// a signed-in caller that can repeat a write indefinitely is a way to
	// fill the operator's disk — the same reasoning as the attachment bounds
	// in internal/conversation, which this had no equivalent of.
	//
	// Messages rather than conversations, because one conversation may hold
	// fifty thousand of them: a ceiling on the count of threads bounds
	// almost nothing. Generous for a person — a heavy year of daily use is
	// some thousands — and reached only by somebody trying.
	MaxStoredMessages = 200000
	// What one account may be storing in characters of message text.
	//
	// The count above bounds rows, and a row may carry some ninety-two thousand
	// characters across its content, reasoning and error: two hundred thousand
	// of them is some eighteen gigabytes behind a ceiling that reads as modest.
	// This is the figure the disk actually feels. Characters rather than
	// bytes, counted the same way on both sides of the comparison; generous
	// for a person (a heavy year is a few megabytes) and reached only by
	// somebody trying.
	MaxStoredChars        = 512 << 20
	MaxTitleChars         = 200
	MaxImportContentChars = conversation.MaxContentChars
	// Reasoning is stored under its own ceiling, above the content one. An
	// import that cut it at the content figure would drop what an export of
	// the same account carries.
	MaxImportReasoningChars = conversation.MaxReasoningChars
)

// exportPageSize is how many conversation rows one read of an export takes.
// An export holds one such page of rows, and the messages of the one
// conversation being written, at a time. A variable so a test can span several
// pages without writing a hundred conversations.
var exportPageSize = 100

var (
	ErrWrongFormat = errors.New("backup: not an Obsidian Arc export")
	// Returned by Import for a document past the limits, and by an export of an
	// account past them: the file it would write is one Import refuses.
	ErrTooLarge = errors.New("backup: this export is larger than the server will import")
	// Distinct from ErrTooLarge: the document is fine, the account is full.
	// Told apart because "make a smaller export" and "delete some
	// conversations first" are different instructions.
	ErrStorageFull = errors.New("backup: this account is storing as many messages as it may")
	// Refused rather than stripped: what landed would not be what was sent.
	// Stored text with a NUL is also the one kind SQL cannot measure (see
	// storedChars), so it is kept out at the door.
	ErrNULCharacter = errors.New("backup: the document contains a NUL character")
)

// Document is the file itself.
type Document struct {
	// Named rather than a bare version number so a file that is not one of
	// ours is refused with something better than a type error.
	Format     int    `json:"obsidian_arc_export"`
	ExportedAt int64  `json:"exported_at"`
	Username   string `json:"username,omitempty"`
	// Whatever the preference document holds, carried opaquely: this package
	// has no business knowing what a wallpaper or an accent is.
	Preferences   json.RawMessage `json:"preferences,omitempty"`
	Conversations []Thread        `json:"conversations"`
}

type Thread struct {
	Title     string `json:"title"`
	Pinned    bool   `json:"pinned,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	Messages  []Turn `json:"messages"`
}

type Turn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
	Error     string `json:"error,omitempty"`
	ModelName string `json:"model_name,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	// How many images the message carried. The pictures themselves are not
	// exported because the server does not keep them (see the attachment
	// retention policy); saying how many were there beats pretending the
	// message was only ever text.
	Images int `json:"images,omitempty"`
}

type Service struct {
	db            *database.DB
	conversations *conversation.Store
	preferences   *user.PreferenceStore
	// The account-wide ceiling. Zero means MaxStoredMessages, which is what
	// every deployment uses; it is a field so a test can reach the boundary
	// without writing two hundred thousand rows to get there.
	MaxStoredMessages int
	// The same for MaxStoredChars.
	MaxStoredChars int64
}

func NewService(db *database.DB, conversations *conversation.Store, preferences *user.PreferenceStore) *Service {
	return &Service{db: db, conversations: conversations, preferences: preferences}
}

// exportStream is an export that has been measured and not yet written. Every
// refusal an export can make is decided in openExport, before the response has
// started: after the first byte the status is 200 and cannot be taken back.
type exportStream struct {
	service  *Service
	account  user.User
	pageSize int
	envelope []byte
}

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
			thread, err := e.service.threadOf(ctx, e.account, record)
			if err != nil {
				return err
			}
			conversations++
			messages += len(thread.Messages)
			if overImportLimits(conversations, messages) {
				return ErrTooLarge
			}
			encoded, err := json.Marshal(thread)
			if err != nil {
				return fmt.Errorf("backup: encode %s: %w", record.ID, err)
			}
			if conversations > 1 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			if _, err := w.Write(encoded); err != nil {
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

// threadOf is one conversation as the document carries it. Its messages are
// read whole, so the memory held for it is one conversation at a time.
//
// Every text field is scrubbed of NUL here rather than left for Import to
// refuse. A conversation can already hold one, because the chat path and
// renaming keep what they were given, and an export that carried it would be a
// file its own account could not restore.
func (s *Service) threadOf(ctx context.Context, account user.User, record conversation.Conversation) (Thread, error) {
	messages, err := s.conversations.Messages(ctx, nil, account.ID, record.ID)
	if err != nil {
		return Thread{}, fmt.Errorf("backup: read %s: %w", record.ID, err)
	}

	out := Thread{
		Title:     scrubNUL(record.Title),
		Pinned:    record.Pinned,
		CreatedAt: record.CreatedAt,
		Messages:  make([]Turn, 0, len(messages)),
	}
	for _, message := range messages {
		out.Messages = append(out.Messages, Turn{
			Role:      scrubNUL(string(message.Role)),
			Content:   scrubNUL(message.Content),
			Reasoning: scrubNUL(message.Reasoning),
			Error:     scrubNUL(message.Error),
			ModelName: scrubNUL(message.ModelName),
			CreatedAt: message.CreatedAt,
			Images:    len(message.Attachments),
		})
	}
	return out, nil
}

// Result reports what an import did, so the interface can say something
// truthful rather than "done".
type Result struct {
	Conversations int  `json:"conversations"`
	Messages      int  `json:"messages"`
	Preferences   bool `json:"preferences"`
	// Threads that carried nothing worth writing. Counted rather than named:
	// a list of two thousand empty titles helps nobody.
	Skipped int `json:"skipped"`
}

// Import writes a document into an account.
//
// Conversations are added, never matched against what is there: two imports
// of the same file produce two copies, which is a duplicate the user can
// delete rather than a merge that ate something.
func (s *Service) Import(ctx context.Context, account user.User, document Document) (Result, error) {
	if document.Format != Format {
		return Result{}, ErrWrongFormat
	}
	total := 0
	for _, thread := range document.Conversations {
		total += len(thread.Messages)
	}
	if overImportLimits(len(document.Conversations), total) {
		return Result{}, ErrTooLarge
	}
	if hasNUL(document) {
		return Result{}, ErrNULCharacter
	}

	// And what the account already holds, checked before anything is
	// written: the per-request limits above say nothing about the twentieth
	// request.
	ceiling := s.ceiling()
	// A first, unlocked look, so a document that plainly does not fit is
	// refused before any of it is written. It is not the enforcement: two
	// imports arriving together both read the same figure here and both pass.
	// importThread re-checks under the account's row lock, which is what the
	// ceiling actually rests on.
	stored, err := s.conversations.CountMessages(ctx, nil, account.ID)
	if err != nil {
		return Result{}, err
	}
	if stored+total > ceiling {
		return Result{}, ErrStorageFull
	}

	// The same question asked of the text itself. Read once, here, and carried
	// through the threads as a running figure: summing an account's messages
	// is a scan of all of them, and doing it inside every thread's transaction
	// would make a two-thousand-conversation import quadratic.
	//
	// That makes this ceiling softer than the count's: a writer that is not
	// this import (a chat turn, another instance's import) can land between
	// the read and the last thread. Each of those is itself bounded — a
	// message holds at most 64 thousand characters, an import 32 MiB — so the
	// overshoot is a request's worth, not unbounded.
	storedChars, err := s.storedChars(ctx, account.ID)
	if err != nil {
		return Result{}, err
	}
	tally := &charTally{ceiling: s.charCeiling(), stored: storedChars}
	if tally.stored+documentChars(document) > tally.ceiling {
		return Result{}, ErrStorageFull
	}

	var result Result

	// Merged rather than replaced: a document from an older release is
	// missing keys this one has, and dropping those to their defaults would
	// be a change the user did not ask for.
	if len(document.Preferences) > 0 {
		var patch map[string]json.RawMessage
		if err := json.Unmarshal(document.Preferences, &patch); err == nil && len(patch) > 0 {
			if _, err := s.preferences.Merge(ctx, account.ID, patch); err != nil {
				return Result{}, fmt.Errorf("backup: restore preferences: %w", err)
			}
			result.Preferences = true
		}
	}

	for _, thread := range document.Conversations {
		written, err := s.importThread(ctx, account, thread, tally)
		if err != nil {
			return result, err
		}
		if written == 0 {
			result.Skipped++
			continue
		}
		result.Conversations++
		result.Messages += written
	}
	return result, nil
}

// hasNUL reports whether any text the import would store carries U+0000. The
// preferences are not walked: they are stored as JSON, which writes a NUL as
// the escape \u0000, so no NUL byte reaches the store through them.
func hasNUL(document Document) bool {
	for _, thread := range document.Conversations {
		if strings.IndexByte(thread.Title, 0) >= 0 {
			return true
		}
		for _, turn := range thread.Messages {
			for _, field := range [...]string{turn.Role, turn.Content, turn.Reasoning, turn.Error, turn.ModelName} {
				if strings.IndexByte(field, 0) >= 0 {
					return true
				}
			}
		}
	}
	return false
}

// overImportLimits is the one place the count limits are compared. Import refuses
// a document for which it holds, and an export refuses an account for which it
// comes to hold; sharing the comparison is what keeps the two from drifting.
func overImportLimits(conversations, messages int) bool {
	return conversations > MaxConversations || messages > MaxMessagesPerImport
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

// ceiling is how many messages one account may store. Zero configures the
// package default.
func (s *Service) ceiling() int {
	if s.MaxStoredMessages > 0 {
		return s.MaxStoredMessages
	}
	return MaxStoredMessages
}

func (s *Service) charCeiling() int64 {
	if s.MaxStoredChars > 0 {
		return s.MaxStoredChars
	}
	return MaxStoredChars
}

// storedChars is the message text an account holds now, in characters.
//
// It is counted in Go rather than with SUM(LENGTH(...)). SQLite's LENGTH stops
// at the first NUL, so text carrying one counts for almost nothing and an
// account could fill itself past the ceiling. Reading every row is the cost of
// an exact count; it is paid once per import, and the rows are read one at a
// time, so only one message is held in memory however large the account.
func (s *Service) storedChars(ctx context.Context, userID string) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT content, reasoning FROM messages WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("backup: measure stored text: %w", err)
	}
	defer rows.Close()

	var chars int64
	for rows.Next() {
		var content, reasoning string
		if err := rows.Scan(&content, &reasoning); err != nil {
			return 0, fmt.Errorf("backup: measure stored text: %w", err)
		}
		chars += int64(utf8.RuneCountInString(content) + utf8.RuneCountInString(reasoning))
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("backup: measure stored text: %w", err)
	}
	return chars, nil
}

// documentChars is an upper bound on what a document would write: the text as
// sent, before the per-message truncation. It is counted in characters like
// the rest of the ceiling. Counting bytes would refuse a CJK document for its
// three bytes a character well before the document reached the ceiling.
func documentChars(document Document) int64 {
	var chars int64
	for _, thread := range document.Conversations {
		for _, turn := range thread.Messages {
			chars += int64(utf8.RuneCountInString(turn.Content) + utf8.RuneCountInString(turn.Reasoning))
		}
	}
	return chars
}

// charTally is one import's running view of the character ceiling: what the
// account held when the import started, plus what its own threads have added.
type charTally struct {
	ceiling int64
	stored  int64
}

// importThread writes one conversation in a transaction, so a file that goes
// wrong halfway leaves whole conversations behind rather than half of one.
func (s *Service) importThread(ctx context.Context, account user.User, thread Thread, tally *charTally) (int, error) {
	usable := make([]Turn, 0, len(thread.Messages))
	for _, turn := range thread.Messages {
		role := conversation.Role(strings.ToLower(strings.TrimSpace(turn.Role)))
		if role != conversation.RoleUser && role != conversation.RoleAssistant {
			continue
		}
		if strings.TrimSpace(turn.Content) == "" && strings.TrimSpace(turn.Error) == "" {
			continue
		}
		usable = append(usable, turn)
	}
	if len(usable) == 0 {
		return 0, nil
	}

	// What will be stored, so the character ceiling is held against the
	// truncated text and not against a megabyte-long field that is about to be
	// cut to size.
	var threadChars int64
	for _, turn := range usable {
		threadChars += int64(min(utf8.RuneCountInString(turn.Content), MaxImportContentChars) +
			min(utf8.RuneCountInString(turn.Reasoning), MaxImportReasoningChars))
	}

	title := text.TrimAndTruncate(thread.Title, MaxTitleChars)
	if title == "" {
		title = conversation.DeriveTitle(usable[0].Content)
	}

	written := 0
	newID := ""
	ceiling := s.ceiling()
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// The stored-message ceiling is read and then written against, so it
		// holds the account's row across both. Without the lock, imports sent
		// in parallel each counted the same pre-import total, each decided
		// they fit, and together wrote several times the cap — the caller's
		// own check before this transaction cannot see the other writers.
		// Same no-op UPDATE as conversation.Store.Upload, for the same reason.
		if _, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, account.ID); err != nil {
			return err
		}
		stored, err := s.conversations.CountMessages(ctx, tx, account.ID)
		if err != nil {
			return err
		}
		if stored+len(usable) > ceiling {
			return ErrStorageFull
		}
		if tally.stored+threadChars > tally.ceiling {
			return ErrStorageFull
		}

		// No model id: the export names the model as text, and an id from
		// another instance would point at a row that is not the same model or
		// does not exist. The name is kept on each message instead.
		created, err := s.conversations.Create(ctx, tx, account.ID,
			conversation.NewConversation{Title: title})
		if err != nil {
			return err
		}

		for _, turn := range usable {
			role := conversation.Role(strings.ToLower(strings.TrimSpace(turn.Role)))
			if _, err := s.conversations.Append(ctx, tx, conversation.AppendInput{
				ConversationID: created.ID,
				UserID:         account.ID,
				Role:           role,
				Content:        text.TrimAndTruncate(turn.Content, MaxImportContentChars),
				Reasoning:      text.TrimAndTruncate(turn.Reasoning, MaxImportReasoningChars),
				Error:          text.TrimAndTruncate(turn.Error, 500),
				ModelName:      text.TrimAndTruncate(turn.ModelName, 80),
			}); err != nil {
				return err
			}
			written++
		}

		newID = created.ID
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("backup: import conversation: %w", err)
	}
	tally.stored += threadChars

	// Outside the transaction because pinning is its own update, and a
	// conversation that arrived unpinned is a cosmetic loss rather than a
	// reason to fail the import.
	if thread.Pinned && newID != "" {
		pinned := true
		if _, err := s.conversations.Update(ctx, account.ID, newID, conversation.Update{Pinned: &pinned}); err != nil {
			return written, nil
		}
	}
	return written, nil
}
