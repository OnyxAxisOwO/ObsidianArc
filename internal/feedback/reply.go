package feedback

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// ReplyInput is one turn about to be written.
type ReplyInput struct {
	FeedbackID string
	// Who is writing, and which side of the conversation they are on.
	// FromStaff is recorded rather than derived later: somebody who answers
	// reports today and loses the grant tomorrow still said it as an operator.
	UserID    string
	FromStaff bool
	Body      string
	// RequireOwner refuses the write unless the thread belongs to UserID. The
	// author's own endpoint sets it; the operator's does not, because an
	// operator answers other people's threads for a living.
	RequireOwner bool
}

// AddReply writes one turn and moves the thread's two unread flags.
//
// The length cap is a check followed by a write, so it holds the thread's own
// row across both — the same lock the daily cap takes on the author's row,
// against the same failure: two replies arriving together, both seeing the
// last free slot.
func (s *Store) AddReply(ctx context.Context, in ReplyInput) (Reply, error) {
	body := strings.TrimSpace(text.Clean(in.Body))
	if body == "" || utf8.RuneCountInString(body) > MaxBodyChars {
		return Reply{}, ErrInvalidBody
	}

	record := Reply{
		ID:         id.New(),
		FeedbackID: in.FeedbackID,
		UserID:     in.UserID,
		FromStaff:  in.FromStaff,
		Body:       body,
	}

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockThread(ctx, tx, in.FeedbackID); err != nil {
			return err
		}

		// Ownership is read inside the lock rather than before it, so a
		// thread cannot be answered on the strength of a check made against a
		// row that has since been deleted.
		var owner string
		if err := tx.QueryRow(ctx,
			`SELECT user_id FROM feedback WHERE id = ?`, in.FeedbackID).Scan(&owner); err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return fmt.Errorf("feedback: read owner: %w", err)
		}
		// Reported as absent rather than as forbidden, which is also all the
		// author's own queries know: they are scoped by owner in the WHERE
		// clause, so there is nothing there that can tell the difference.
		if in.RequireOwner && owner != in.UserID {
			return ErrNotFound
		}

		var count int
		var latest int64
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*), COALESCE(MAX(created_at), 0) FROM feedback_replies WHERE feedback_id = ?`,
			in.FeedbackID).Scan(&count, &latest); err != nil {
			return fmt.Errorf("feedback: count replies: %w", err)
		}
		if count >= MaxRepliesPerThread {
			return ErrThreadFull
		}

		// Stamped once the row lock is held, and never at or before the
		// thread's latest turn: two turns in one millisecond would otherwise
		// tie, and a tie has no order the conversation can be read back in.
		// Stamped before the lock, a turn that queued behind another could
		// also commit with an older time than the one that committed first.
		now := time.Now().UnixMilli()
		if now <= latest {
			now = latest + 1
		}
		record.CreatedAt = now
		if _, err := tx.Exec(ctx, `INSERT INTO feedback_replies
			(id, feedback_id, user_id, from_staff, body, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			record.ID, record.FeedbackID, record.UserID, record.FromStaff,
			record.Body, record.CreatedAt); err != nil {
			return fmt.Errorf("feedback: insert reply: %w", err)
		}

		// Whoever just wrote has by definition read everything before it, so
		// one statement sets both flags: the other side is now owed a look,
		// and this side is not.
		_, err := tx.Exec(ctx,
			`UPDATE feedback SET author_unread = ?, operator_unread = ?, updated_at = ? WHERE id = ?`,
			in.FromStaff, !in.FromStaff, now, in.FeedbackID)
		if err != nil {
			return fmt.Errorf("feedback: mark unread: %w", err)
		}

		// Only the operator's half is a notice: the author's own replies land
		// where the operator already reads everything, on the backoffice's own
		// unread flag above, not in an inbox meant for the other side.
		if in.FromStaff && s.Notify != nil {
			if err := s.Notify.Push(ctx, tx, notify.Notification{
				Audience: notify.AudienceUser, UserID: owner,
				Kind: "feedback_reply", Link: "/feedback",
			}); err != nil {
				return fmt.Errorf("feedback: notify: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return Reply{}, err
	}
	return record, nil
}

// Replies is one thread's turns, oldest first — the order they were said in,
// which is the only order a conversation reads in.
func (s *Store) Replies(ctx context.Context, q database.Queryer, feedbackID string) ([]Reply, error) {
	rows, err := s.pick(q).Query(ctx,
		`SELECT `+replyColumns+`, u.username, u.nickname
		 FROM feedback_replies p JOIN users u ON u.id = p.user_id
		 WHERE p.feedback_id = ? ORDER BY p.created_at, p.id`, feedbackID)
	if err != nil {
		return nil, fmt.Errorf("feedback: replies: %w", err)
	}
	defer rows.Close()

	out := []Reply{}
	for rows.Next() {
		var record Reply
		if err := scanReply(rows, &record, &record.Username, &record.Nickname); err != nil {
			return nil, fmt.Errorf("feedback: scan reply: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// Thread is one report and everything said about it.
//
// ownerID scopes it to one author — the reader's own endpoint passes theirs,
// and the operator's passes "" — so a thread that belongs to somebody else is
// reported as absent rather than as forbidden, which is the same answer the
// author's other queries give.
func (s *Store) Thread(ctx context.Context, q database.Queryer, feedbackID, ownerID string) (Thread, error) {
	record, err := s.ByID(ctx, q, feedbackID)
	if err != nil {
		return Thread{}, err
	}
	if ownerID != "" && record.UserID != ownerID {
		return Thread{}, ErrNotFound
	}
	replies, err := s.Replies(ctx, q, feedbackID)
	if err != nil {
		return Thread{}, err
	}
	return Thread{Feedback: record, Replies: replies}, nil
}

// OpenThread reads one thread and clears the unread flag of the side reading
// it, in one transaction. As two statements, a reply that committed between the
// read and the clear set its flag and was then cleared without the reader ever
// receiving it. The thread row lock is the one AddReply takes, so a reply either
// commits before the read and is in the response, or after the clear and leaves
// its flag set. staff selects the operator's flag; ownerID scopes the read as
// Thread does, and "" is the operator's unscoped view.
func (s *Store) OpenThread(ctx context.Context, feedbackID, ownerID string, staff bool) (Thread, error) {
	var thread Thread
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockThread(ctx, tx, feedbackID); err != nil {
			return err
		}

		var err error
		thread, err = s.Thread(ctx, tx, feedbackID, ownerID)
		if err != nil {
			return err
		}
		unread, column := thread.Feedback.AuthorUnread, "author_unread"
		if staff {
			unread, column = thread.Feedback.OperatorUnread, "operator_unread"
		}
		if !unread {
			return nil
		}
		// updated_at stays as it is: reading is not a change, and the operator's
		// list shows that column.
		if _, err := tx.Exec(ctx,
			`UPDATE feedback SET `+column+` = ? WHERE id = ?`, false, feedbackID); err != nil {
			return fmt.Errorf("feedback: mark seen: %w", err)
		}
		if staff {
			thread.Feedback.OperatorUnread = false
		} else {
			thread.Feedback.AuthorUnread = false
		}
		return nil
	})
	if err != nil {
		return Thread{}, err
	}
	return thread, nil
}

// lockThread takes the thread's row lock with a no-op UPDATE, the portable
// spelling the repository settles on, so every read-then-write on one thread
// (a reply, a reader clearing its flag, a deletion moving the flags back)
// runs one after another across instances. A thread that is gone locks no
// row and reads as ErrNotFound.
func lockThread(ctx context.Context, tx *database.Tx, feedbackID string) error {
	locked, err := tx.Exec(ctx,
		`UPDATE feedback SET updated_at = updated_at WHERE id = ?`, feedbackID)
	if err != nil {
		return fmt.Errorf("feedback: lock thread: %w", err)
	}
	if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteReply removes one turn. For spam inside an otherwise real thread —
// deleting the whole report is the other, blunter answer.
//
// The unread flags describe the newest turn, so only deleting that turn moves
// them. Deleting an older one leaves the conversation's last word where it was.
func (s *Store) DeleteReply(ctx context.Context, feedbackID, replyID string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockThread(ctx, tx, feedbackID); err != nil {
			return err
		}

		var newestID string
		var newestStaff bool
		if err := tx.QueryRow(ctx,
			`SELECT id, from_staff FROM feedback_replies WHERE feedback_id = ?
			 ORDER BY created_at DESC, id DESC LIMIT 1`, feedbackID).Scan(&newestID, &newestStaff); err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return fmt.Errorf("feedback: read newest reply: %w", err)
		}

		result, err := tx.Exec(ctx,
			`DELETE FROM feedback_replies WHERE id = ? AND feedback_id = ?`, replyID, feedbackID)
		if err != nil {
			return fmt.Errorf("feedback: delete reply: %w", err)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
			return ErrNotFound
		}
		if newestID != replyID {
			return nil
		}

		// Whoever wrote the deleted turn had read everything before it, as
		// AddReply assumes of its writer. If the turn now last was written by
		// the same side, the other side is owed a look at it, which is what
		// AddReply would have set. If it was written by the other side, the
		// deleted turn's writer has read it and nothing is owed to either
		// side. A report with no turns left has both flags clear, as Create
		// leaves it.
		var lastStaff bool
		author, operator := false, false
		err = tx.QueryRow(ctx,
			`SELECT from_staff FROM feedback_replies WHERE feedback_id = ?
			 ORDER BY created_at DESC, id DESC LIMIT 1`, feedbackID).Scan(&lastStaff)
		switch {
		case err == nil && lastStaff == newestStaff:
			author, operator = newestStaff, !newestStaff
		case err != nil && !database.IsNotFound(err):
			return fmt.Errorf("feedback: read last reply: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE feedback SET author_unread = ?, operator_unread = ?, updated_at = ? WHERE id = ?`,
			author, operator, time.Now().UnixMilli(), feedbackID); err != nil {
			return fmt.Errorf("feedback: recompute unread: %w", err)
		}
		return nil
	})
}
