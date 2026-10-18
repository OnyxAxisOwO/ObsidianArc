package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Result reports what an import did, so the interface can say something
// truthful rather than "done".
type Result struct {
	Conversations int  `json:"conversations"`
	Messages      int  `json:"messages"`
	Preferences   bool `json:"preferences"`
	// Threads that carried nothing worth writing. Counted rather than named:
	// a list of two thousand empty titles helps nobody.
	Skipped int `json:"skipped"`
	// Conversations that were written but could not be pinned. They are in the
	// account unpinned, and the count tells the reader there are some to find.
	Unpinned int `json:"unpinned"`
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
	// Only the turns importThread will write count. A file whose extra
	// messages are blank, or of a role this import does not keep, is not too
	// large for what it holds. The characters come from the same plan, so the
	// character ceiling is checked against the text that will be stored and
	// not against the text the file happens to carry.
	total := 0
	var chars int64
	for _, thread := range document.Conversations {
		plan := planThread(thread)
		total += len(plan.turns)
		chars += plan.chars
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
	// A document with no messages writes none, so the account is not measured
	// for it. Otherwise every empty import would scan the account's rows for
	// nothing, and a signed-in account could repeat that scan at will.
	ceiling := s.ceiling()
	if total > 0 {
		// A first, unlocked look, so a document that plainly does not fit is
		// refused before any of it is written. It is not the enforcement: two
		// imports arriving together both read the same figure here and both
		// pass. importThread re-checks under the account's row lock, which is
		// what the ceiling actually rests on.
		stored, err := s.conversations.CountMessages(ctx, nil, account.ID)
		if err != nil {
			return Result{}, err
		}
		if stored+total > ceiling {
			return Result{}, ErrStorageFull
		}
	}

	// The same question asked of the text itself. Read once, here, and carried
	// through the threads as a running figure: summing an account's messages
	// is a scan of all of them, and doing it inside every thread's transaction
	// would make a two-thousand-conversation import quadratic.
	//
	// That makes this ceiling softer than the count's: a writer that is not
	// this import (a chat turn, another instance's import) can land between
	// the read and the last thread. Each of those is itself bounded — a
	// message holds at most the per-field limits the conversation package sets,
	// and an import 32 MiB — so the overshoot is a request's worth, not
	// unbounded. A document with no text writes none, so the scan is skipped
	// for it, as the count above is.
	tally := &charTally{ceiling: s.charCeiling()}
	if chars > 0 {
		storedChars, err := s.storedChars(ctx, account.ID)
		if err != nil {
			return Result{}, err
		}
		tally.stored = storedChars
		if tally.stored+chars > tally.ceiling {
			return Result{}, ErrStorageFull
		}
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
		written, unpinned, err := s.importThread(ctx, account, thread, tally)
		if err != nil {
			return result, err
		}
		if written == 0 {
			result.Skipped++
			continue
		}
		result.Conversations++
		result.Messages += written
		if unpinned {
			result.Unpinned++
		}
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
			for _, field := range [...]string{turn.Role, turn.Content, turn.Reasoning, turn.Error, turn.ErrorCode, turn.ModelName} {
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

// usableTurn reports whether a turn is one the import keeps on its own. The
// thread it belongs to can still be dropped whole by importedTurns.
func usableTurn(turn Turn) bool {
	role := conversation.Role(strings.ToLower(strings.TrimSpace(turn.Role)))
	if role != conversation.RoleUser && role != conversation.RoleAssistant {
		return false
	}
	// A question is kept even with no text. An image sent on its own has no
	// words, but the answer after it was written in reply to it, and importing
	// that answer alone would leave it with nothing in front of it. Import attaches
	// no images, so the question comes back empty. A re-export writes it back as
	// an empty user turn, which this keeps again, so the count holds across
	// generations.
	//
	// An answer with nothing in it is dropped. A stopped answer that produced only
	// reasoning is not empty, and is kept: it is still an answer to the question
	// before it.
	if role == conversation.RoleAssistant && !hasText(turn) {
		return false
	}
	return true
}

// importedTurns is what importThread writes from a thread, in order, and what
// the size checks count. A thread with no text anywhere in it is not written:
// its only turns would be empty questions, and restoring those would produce
// conversations with nothing in them.
func importedTurns(thread Thread) []Turn {
	usable := make([]Turn, 0, len(thread.Messages))
	anyText := false
	for _, turn := range thread.Messages {
		if !usableTurn(turn) {
			continue
		}
		usable = append(usable, turn)
		anyText = anyText || hasText(turn)
	}
	if !anyText {
		return nil
	}
	return usable
}

// hasText reports whether a turn has words to restore. An image is not text:
// import attaches no images, so an image-only turn has nothing to write.
func hasText(turn Turn) bool {
	return strings.TrimSpace(turn.Content) != "" ||
		strings.TrimSpace(turn.Reasoning) != "" ||
		strings.TrimSpace(turn.Error) != ""
}

// Caps on the two labels an imported turn carries. They are short names, and a
// file can carry any length, so the import cuts them rather than writing them
// as they came.
const (
	maxImportErrorCodeChars = 64
	maxImportModelNameChars = 80
)

// threadPlan is what importing one thread stores: its title, the turns the
// import keeps, and the characters those come to once each field is cut to the
// length the write cuts it to. The size pre-check and the transaction both read
// it, so the ceiling is held against the same text on both sides.
type threadPlan struct {
	title string
	turns []Turn
	chars int64
}

// planThread works out what importing a thread would write. A thread with
// nothing to write has an empty plan.
func planThread(thread Thread) threadPlan {
	turns := importedTurns(thread)
	if len(turns) == 0 {
		return threadPlan{}
	}
	title := text.TrimAndTruncate(thread.Title, MaxTitleChars)
	if title == "" {
		// The first turn may be an image with no words to name the thread by.
		for _, turn := range turns {
			if strings.TrimSpace(turn.Content) != "" {
				title = conversation.DeriveTitle(turn.Content)
				break
			}
		}
	}
	chars := int64(utf8.RuneCountInString(title))
	for _, turn := range turns {
		chars += int64(utf8.RuneCountInString(text.TrimAndTruncate(turn.Content, MaxImportContentChars)) +
			utf8.RuneCountInString(text.TrimAndTruncate(turn.Reasoning, MaxImportReasoningChars)) +
			utf8.RuneCountInString(text.TrimAndTruncate(turn.Error, conversation.MaxErrorChars)))
	}
	return threadPlan{title: title, turns: turns, chars: chars}
}

// importThread writes one conversation in a transaction, so a file that goes
// wrong halfway leaves whole conversations behind rather than half of one. It
// returns how many turns it wrote, and whether the conversation was written but
// could not be pinned.
func (s *Service) importThread(ctx context.Context, account user.User, thread Thread, tally *charTally) (int, bool, error) {
	plan := planThread(thread)
	if len(plan.turns) == 0 {
		return 0, false, nil
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
		// Counted afresh in every conversation's transaction, so an import of
		// N conversations into an account holding S messages costs N indexed
		// counts of S — the quadratic shape the character tally avoids. It is
		// kept on purpose: a tally read once before the import would be the
		// soft ceiling the characters have, and two imports racing would each
		// pass against the same stale total. With the caps above, N×S stays in
		// the millions for any account in real use.
		stored, err := s.conversations.CountMessages(ctx, tx, account.ID)
		if err != nil {
			return err
		}
		if stored+len(plan.turns) > ceiling {
			return ErrStorageFull
		}
		if tally.stored+plan.chars > tally.ceiling {
			return ErrStorageFull
		}

		// No model id: the export names the model as text, and an id from
		// another instance would point at a row that is not the same model or
		// does not exist. The name is kept on each message instead.
		created, err := s.conversations.Create(ctx, tx, account.ID,
			conversation.NewConversation{Title: plan.title})
		if err != nil {
			return err
		}

		for _, turn := range plan.turns {
			role := conversation.Role(strings.ToLower(strings.TrimSpace(turn.Role)))
			if _, err := s.conversations.Append(ctx, tx, conversation.AppendInput{
				ConversationID: created.ID,
				UserID:         account.ID,
				Role:           role,
				Content:        text.TrimAndTruncate(turn.Content, MaxImportContentChars),
				Reasoning:      text.TrimAndTruncate(turn.Reasoning, MaxImportReasoningChars),
				Error:          text.TrimAndTruncate(turn.Error, conversation.MaxErrorChars),
				ErrorCode:      text.TrimAndTruncate(turn.ErrorCode, maxImportErrorCodeChars),
				ModelName:      text.TrimAndTruncate(turn.ModelName, maxImportModelNameChars),
			}); err != nil {
				return err
			}
			written++
		}

		newID = created.ID
		return nil
	})
	if err != nil {
		return 0, false, fmt.Errorf("backup: import conversation: %w", err)
	}
	tally.stored += plan.chars

	// Outside the transaction because pinning is its own update, and a
	// conversation that arrived unpinned is a cosmetic loss rather than a
	// reason to fail the import. It is still reported, so the reader knows
	// which of the conversations they brought in are not where they were.
	if thread.Pinned && newID != "" {
		if err := s.pinConversation(ctx, account.ID, newID); err != nil {
			slog.ErrorContext(ctx, "backup: pin imported conversation", "conversation", newID, "error", err)
			return written, true, nil
		}
	}
	return written, false, nil
}
