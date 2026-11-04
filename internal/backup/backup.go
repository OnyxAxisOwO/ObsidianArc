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
	"context"
	"encoding/json"
	"errors"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
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
	// The count above bounds rows, and a row may carry the conversation package's
	// per-field limits across its content, reasoning and error. Multiplied by the
	// row count, that is the figure the disk is measured against: the row count
	// alone reads as modest and hides it.
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
	// The stable code behind Error, so an import can be shown in the reader's
	// language. Files written before it existed have none and keep the sentence.
	ErrorCode string `json:"error_code,omitempty"`
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
	// Pins an imported conversation. A field so a test can make it fail: the
	// update succeeds in practice, so nothing else reaches that branch.
	pinConversation func(ctx context.Context, userID, conversationID string) error
}

func NewService(db *database.DB, conversations *conversation.Store, preferences *user.PreferenceStore) *Service {
	s := &Service{db: db, conversations: conversations, preferences: preferences}
	s.pinConversation = func(ctx context.Context, userID, conversationID string) error {
		pinned := true
		_, err := conversations.Update(ctx, userID, conversationID, conversation.Update{Pinned: &pinned})
		return err
	}
	return s
}
