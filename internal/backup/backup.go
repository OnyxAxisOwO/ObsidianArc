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
	"fmt"
	"strings"
	"time"

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
	MaxStoredMessages     = 200000
	MaxTitleChars         = 200
	MaxImportContentChars = conversation.MaxContentChars
)

var (
	ErrWrongFormat = errors.New("backup: not an Obsidian Arc export")
	ErrTooLarge    = errors.New("backup: this export is larger than the server will import")
	// Distinct from ErrTooLarge: the document is fine, the account is full.
	// Told apart because "make a smaller export" and "delete some
	// conversations first" are different instructions.
	ErrStorageFull = errors.New("backup: this account is storing as many messages as it may")
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
}

func NewService(db *database.DB, conversations *conversation.Store, preferences *user.PreferenceStore) *Service {
	return &Service{db: db, conversations: conversations, preferences: preferences}
}

// Export gathers one account's conversations and preferences.
//
// Every message of every conversation is read, which is a lot of small
// queries for a heavy account — acceptable because this runs when a person
// presses a button, not on any hot path.
func (s *Service) Export(ctx context.Context, account user.User) (Document, error) {
	document := Document{
		Format:        Format,
		ExportedAt:    time.Now().UnixMilli(),
		Username:      account.Username,
		Conversations: []Thread{},
	}

	// Already a JSON document in the store, carried across as it is.
	if preferences, err := s.preferences.Get(ctx, account.ID); err == nil && len(preferences) > 0 {
		document.Preferences = preferences
	}

	threads, err := s.conversations.List(ctx, account.ID, MaxConversations)
	if err != nil {
		return Document{}, fmt.Errorf("backup: list conversations: %w", err)
	}

	for _, thread := range threads {
		messages, err := s.conversations.Messages(ctx, nil, account.ID, thread.ID)
		if err != nil {
			return Document{}, fmt.Errorf("backup: read %s: %w", thread.ID, err)
		}

		out := Thread{
			Title:     thread.Title,
			Pinned:    thread.Pinned,
			CreatedAt: thread.CreatedAt,
			Messages:  make([]Turn, 0, len(messages)),
		}
		for _, message := range messages {
			out.Messages = append(out.Messages, Turn{
				Role:      string(message.Role),
				Content:   message.Content,
				Reasoning: message.Reasoning,
				Error:     message.Error,
				ModelName: message.ModelName,
				CreatedAt: message.CreatedAt,
				Images:    len(message.Attachments),
			})
		}
		document.Conversations = append(document.Conversations, out)
	}
	return document, nil
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
	if len(document.Conversations) > MaxConversations {
		return Result{}, ErrTooLarge
	}

	total := 0
	for _, thread := range document.Conversations {
		total += len(thread.Messages)
	}
	if total > MaxMessagesPerImport {
		return Result{}, ErrTooLarge
	}

	// And what the account already holds, checked before anything is
	// written: the per-request limits above say nothing about the twentieth
	// request.
	ceiling := s.MaxStoredMessages
	if ceiling <= 0 {
		ceiling = MaxStoredMessages
	}
	stored, err := s.conversations.CountMessages(ctx, account.ID)
	if err != nil {
		return Result{}, err
	}
	if stored+total > ceiling {
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
		written, err := s.importThread(ctx, account, thread)
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

// importThread writes one conversation in a transaction, so a file that goes
// wrong halfway leaves whole conversations behind rather than half of one.
func (s *Service) importThread(ctx context.Context, account user.User, thread Thread) (int, error) {
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

	title := text.TrimAndTruncate(thread.Title, MaxTitleChars)
	if title == "" {
		title = conversation.DeriveTitle(usable[0].Content)
	}

	written := 0
	newID := ""
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// No model id: the export names the model as text, and an id from
		// another instance would point at a row that is not the same model or
		// does not exist. The name is kept on each message instead.
		created, err := s.conversations.Create(ctx, tx, account.ID, title, "")
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
				Reasoning:      text.TrimAndTruncate(turn.Reasoning, MaxImportContentChars),
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
