// Package feedback is what the people using the instance have to say about
// it: a defect they hit, or something they would like it to do.
//
// Rows rather than a mail relay, for the same reason announcements are rows:
// "what have people told us, and what did we do about it" is worth being
// able to look up afterwards, and a report that arrives in one operator's
// mailbox is a report the next operator never sees.
//
// A report is a conversation: both sides reply, and both write Markdown. It
// is stored exactly as typed and rendered by `chat/markdown.ts`, which builds
// nodes and never assembles an HTML string — so there is nothing to sanitise
// on the way in, and a report written by a stranger is safe to render in the
// operator's own screen. That renderer also declines to emit <img>, which is
// what stops a reported "bug" from turning every operator who reads it into a
// hit on somebody's tracker.
package feedback

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// Kind is what the reader is telling us: something is broken, or something
// could be better. Two, because a third ("question") would be a support
// channel, and this is not one.
type Kind string

const (
	KindBug  Kind = "bug"
	KindIdea Kind = "idea"
)

var Kinds = []Kind{KindBug, KindIdea}

func (k Kind) Valid() bool {
	for _, candidate := range Kinds {
		if k == candidate {
			return true
		}
	}
	return false
}

// Priority is the reporter's own estimate of how much it matters. It is
// deliberately theirs rather than the operator's: what an operator thinks
// belongs in Status, and the gap between the two is the interesting part.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
)

var Priorities = []Priority{PriorityLow, PriorityMedium, PriorityHigh}

func (p Priority) Valid() bool {
	for _, candidate := range Priorities {
		if p == candidate {
			return true
		}
	}
	return false
}

// Status is the operator's half. Two values, not a workflow: a report has
// either been dealt with or it has not, and every extra state is a state
// somebody has to remember to move things out of.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

var Statuses = []Status{StatusOpen, StatusResolved}

func (s Status) Valid() bool {
	for _, candidate := range Statuses {
		if s == candidate {
			return true
		}
	}
	return false
}

const (
	MaxTitleChars = 120
	// Long enough for reproduction steps and a stack trace pasted in, short
	// enough that one report cannot be a denial-of-service on the table.
	MaxBodyChars = 8000
	// Per account, per rolling day. High enough that nobody reporting real
	// defects will meet it, low enough that a bored account cannot fill the
	// operator's list faster than it can be read.
	MaxPerDay = 10
	// Per thread, both sides together. A conversation that has run this long
	// is one that should have become a defect somewhere else, and the cap is
	// what stops a thread growing without bound in a table everybody's list
	// query touches.
	MaxRepliesPerThread = 50
)

// DayWindow is the span MaxPerDay is counted over.
const DayWindow = 24 * time.Hour

var (
	ErrNotFound      = errors.New("feedback: not found")
	ErrInvalidTitle  = errors.New("feedback: title must be 1-120 characters")
	ErrInvalidBody   = errors.New("feedback: body must be 1-8000 characters")
	ErrInvalidKind   = errors.New("feedback: unknown kind")
	ErrInvalidPrio   = errors.New("feedback: unknown priority")
	ErrInvalidStatus = errors.New("feedback: unknown status")
	ErrTooMany       = errors.New("feedback: this account has sent its daily maximum")
	ErrThreadFull    = errors.New("feedback: this conversation has reached its maximum length")
)

type Feedback struct {
	ID        string   `json:"id"`
	UserID    string   `json:"user_id"`
	Kind      Kind     `json:"kind"`
	Priority  Priority `json:"priority"`
	Status    Status   `json:"status"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`

	// How long the conversation is, and whether the side reading this list
	// has seen its last word. AuthorUnread is what puts a dot on the reader's
	// account menu; OperatorUnread is what marks a thread as waiting for an
	// answer in the backoffice.
	Replies        int  `json:"replies"`
	AuthorUnread   bool `json:"author_unread"`
	OperatorUnread bool `json:"operator_unread"`

	// Filled for the operator's listing, empty for the author's own — they
	// know who they are, and the join is not free.
	Username string `json:"username,omitempty"`
	Nickname string `json:"nickname,omitempty"`
}

// Reply is one turn in a thread. Both sides write Markdown, and it is stored
// exactly as typed: it is rendered by the same node-building renderer the
// transcript uses, which never assembles an HTML string, so there is nothing
// here to sanitise on the way in and nothing to regret on the way out.
type Reply struct {
	ID         string `json:"id"`
	FeedbackID string `json:"feedback_id"`
	UserID     string `json:"user_id"`
	FromStaff  bool   `json:"from_staff"`
	Body       string `json:"body"`
	CreatedAt  int64  `json:"created_at"`

	Username string `json:"username,omitempty"`
	Nickname string `json:"nickname,omitempty"`
}

// Thread is a report and everything said about it since.
type Thread struct {
	Feedback Feedback `json:"feedback"`
	Replies  []Reply  `json:"replies"`
}

type Store struct {
	db *database.DB
	// Set by the wiring, never by NewStore: nil is what every existing test
	// and every instance that predates this feature gets, and nil means
	// exactly "push nothing" rather than a nil-pointer panic waiting for a
	// caller that forgot it.
	Notify *notify.Store
}

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const columns = `f.id, f.user_id, f.kind, f.priority, f.status, f.title, f.body,
	f.created_at, f.updated_at, f.author_unread, f.operator_unread,
	(SELECT COUNT(*) FROM feedback_replies r WHERE r.feedback_id = f.id)`

// Counted rather than kept in a column on the row: a counter maintained by
// hand is a counter that drifts, and this one is read by queries that are
// already touching the table it counts.
const replyColumns = `p.id, p.feedback_id, p.user_id, p.from_staff, p.body, p.created_at`

type rowScanner interface{ Scan(dest ...any) error }

// scanFeedback reads `columns` in order, with whatever the caller's query
// appended after them — one place that knows the order, rather than three
// that have to be corrected together every time a column is added.
func scanFeedback(row rowScanner, record *Feedback, extra ...any) error {
	dest := []any{
		&record.ID, &record.UserID, &record.Kind, &record.Priority, &record.Status,
		&record.Title, &record.Body, &record.CreatedAt, &record.UpdatedAt,
		&record.AuthorUnread, &record.OperatorUnread, &record.Replies,
	}
	return row.Scan(append(dest, extra...)...)
}

func scanReply(row rowScanner, record *Reply, extra ...any) error {
	dest := []any{
		&record.ID, &record.FeedbackID, &record.UserID,
		&record.FromStaff, &record.Body, &record.CreatedAt,
	}
	return row.Scan(append(dest, extra...)...)
}

func (s *Store) pick(q database.Queryer) database.Queryer {
	if q == nil {
		return s.db
	}
	return q
}

type Input struct {
	Kind     Kind
	Priority Priority
	Title    string
	Body     string
}

// The text is cleaned before it is counted or stored: PostgreSQL refuses a NUL
// byte, and the insert would fail as a 500 with the author's words lost.
func validate(in Input) (Input, error) {
	in.Title = strings.TrimSpace(text.Clean(in.Title))
	if in.Title == "" || utf8.RuneCountInString(in.Title) > MaxTitleChars {
		return Input{}, ErrInvalidTitle
	}
	in.Body = strings.TrimSpace(text.Clean(in.Body))
	if in.Body == "" || utf8.RuneCountInString(in.Body) > MaxBodyChars {
		return Input{}, ErrInvalidBody
	}
	if in.Kind == "" {
		in.Kind = KindBug
	}
	if !in.Kind.Valid() {
		return Input{}, ErrInvalidKind
	}
	if in.Priority == "" {
		in.Priority = PriorityMedium
	}
	if !in.Priority.Valid() {
		return Input{}, ErrInvalidPrio
	}
	return in, nil
}

// Create records one report for userID.
//
// The daily cap and the insert share a transaction that locks the author's
// row first, for the reason apikey.Store.Issue and project.Store.Create do
// the same: a count followed by a standalone insert lets two parallel
// requests both see the last free slot and take it, and the deployment notes
// allow a second server process against one database, so a mutex here would
// only serialise half of them.
func (s *Store) Create(ctx context.Context, userID string, in Input) (Feedback, error) {
	in, err := validate(in)
	if err != nil {
		return Feedback{}, err
	}

	now := time.Now().UnixMilli()
	record := Feedback{
		ID:        id.New(),
		UserID:    userID,
		Kind:      in.Kind,
		Priority:  in.Priority,
		Status:    StatusOpen,
		Title:     in.Title,
		Body:      in.Body,
		CreatedAt: now,
		UpdatedAt: now,
	}

	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID)
		if err != nil {
			return fmt.Errorf("feedback: lock author: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return ErrNotFound
		}

		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM feedback WHERE user_id = ? AND created_at > ?`,
			userID, now-DayWindow.Milliseconds()).Scan(&count); err != nil {
			return fmt.Errorf("feedback: count recent: %w", err)
		}
		if count >= MaxPerDay {
			return ErrTooMany
		}

		_, err = tx.Exec(ctx, `INSERT INTO feedback
			(id, user_id, kind, priority, status, title, body, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.UserID, record.Kind, record.Priority, record.Status,
			record.Title, record.Body, record.CreatedAt, record.UpdatedAt)
		if err != nil {
			return fmt.Errorf("feedback: insert: %w", err)
		}

		// In the same transaction as the report it is about: an operator must
		// never be told about a report that the write below it rolled back.
		if s.Notify != nil {
			if err := s.Notify.Push(ctx, tx, notify.Notification{
				Audience: notify.AudienceAdmins, Permission: "feedback",
				Kind: "feedback_new", Params: map[string]any{"title": record.Title},
				Link: "/admin/feedback/" + record.ID,
			}); err != nil {
				return fmt.Errorf("feedback: notify: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return Feedback{}, err
	}
	return record, nil
}

// Filter is what the operator's list is narrowed by. Every field is
// optional, and an unknown enum value narrows to nothing rather than being
// ignored — a filter that silently matches everything is worse than one that
// visibly matches nothing.
type Filter struct {
	Kind     Kind
	Priority Priority
	Status   Status
	UserID   string
	Search   string
	Limit    int
	Offset   int
}

func (f Filter) where() (string, []any) {
	clauses := []string{}
	args := []any{}
	if f.Kind != "" {
		clauses = append(clauses, "f.kind = ?")
		args = append(args, string(f.Kind))
	}
	if f.Priority != "" {
		clauses = append(clauses, "f.priority = ?")
		args = append(args, string(f.Priority))
	}
	if f.Status != "" {
		clauses = append(clauses, "f.status = ?")
		args = append(args, string(f.Status))
	}
	if f.UserID != "" {
		clauses = append(clauses, "f.user_id = ?")
		args = append(args, f.UserID)
	}
	if search := strings.TrimSpace(f.Search); search != "" {
		// LOWER(...) LIKE rather than ILIKE: one of the two engines here has
		// never heard of ILIKE, and the migration lint says so. The typed text
		// is a literal, so its wildcards are escaped: a search for "_" must
		// not match every title, and the count has to agree with the list.
		pattern := "%" + database.EscapeLike(strings.ToLower(search)) + "%"
		clauses = append(clauses, `(LOWER(f.title) LIKE ? ESCAPE '\' OR LOWER(f.body) LIKE ? ESCAPE '\' OR LOWER(u.username) LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// List is the operator's view: every report, newest first, with the author
// resolved. total is the count before the page window, for the pager.
func (s *Store) List(ctx context.Context, q database.Queryer, filter Filter) (records []Feedback, total int, err error) {
	where, args := filter.where()
	queryer := s.pick(q)

	if err := queryer.QueryRow(ctx,
		`SELECT COUNT(*) FROM feedback f JOIN users u ON u.id = f.user_id`+where,
		args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("feedback: count: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	rows, err := queryer.Query(ctx,
		`SELECT `+columns+`, u.username, u.nickname
		 FROM feedback f JOIN users u ON u.id = f.user_id`+where+
			` ORDER BY f.created_at DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("feedback: list: %w", err)
	}
	defer rows.Close()

	records = []Feedback{}
	for rows.Next() {
		var record Feedback
		if err := scanFeedback(rows, &record, &record.Username, &record.Nickname); err != nil {
			return nil, 0, fmt.Errorf("feedback: scan: %w", err)
		}
		records = append(records, record)
	}
	return records, total, rows.Err()
}

// ListFor is what an author sees: their own reports and their standing, so
// sending one twice because the first appeared to vanish is not a thing that
// happens.
//
// The newest fifty, plus every report still holding an answer the author has
// not opened. The account-menu dot counts all of those, and the only way to
// open one is to pick it from this list, so one older than the fifty would
// keep the dot lit with no way to clear it.
func (s *Store) ListFor(ctx context.Context, q database.Queryer, userID string) ([]Feedback, error) {
	rows, err := s.pick(q).Query(ctx,
		`SELECT `+columns+` FROM feedback f
		 WHERE f.user_id = ? AND (f.author_unread = ? OR f.id IN (
		   SELECT id FROM feedback WHERE user_id = ? ORDER BY created_at DESC LIMIT 50))
		 ORDER BY f.created_at DESC`,
		userID, true, userID)
	if err != nil {
		return nil, fmt.Errorf("feedback: list for author: %w", err)
	}
	defer rows.Close()

	out := []Feedback{}
	for rows.Next() {
		var record Feedback
		if err := scanFeedback(rows, &record); err != nil {
			return nil, fmt.Errorf("feedback: scan: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) ByID(ctx context.Context, q database.Queryer, feedbackID string) (Feedback, error) {
	var record Feedback
	err := scanFeedback(s.pick(q).QueryRow(ctx,
		`SELECT `+columns+`, u.username, u.nickname
		 FROM feedback f JOIN users u ON u.id = f.user_id WHERE f.id = ?`, feedbackID),
		&record, &record.Username, &record.Nickname)
	if err != nil {
		if database.IsNotFound(err) {
			return Feedback{}, ErrNotFound
		}
		return Feedback{}, fmt.Errorf("feedback: by id: %w", err)
	}
	return record, nil
}

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
		locked, err := tx.Exec(ctx,
			`UPDATE feedback SET updated_at = updated_at WHERE id = ?`, in.FeedbackID)
		if err != nil {
			return fmt.Errorf("feedback: lock thread: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return ErrNotFound
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
		_, err = tx.Exec(ctx,
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
		locked, err := tx.Exec(ctx,
			`UPDATE feedback SET updated_at = updated_at WHERE id = ?`, feedbackID)
		if err != nil {
			return fmt.Errorf("feedback: lock thread: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return ErrNotFound
		}

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

// UnreadFor is the dot on one reader's account menu: how many of their own
// reports have an answer they have not opened yet.
func (s *Store) UnreadFor(ctx context.Context, q database.Queryer, userID string) (int, error) {
	var count int
	err := s.pick(q).QueryRow(ctx,
		`SELECT COUNT(*) FROM feedback WHERE user_id = ? AND author_unread = ?`,
		userID, true).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("feedback: unread: %w", err)
	}
	return count, nil
}

// DeleteReply removes one turn. For spam inside an otherwise real thread —
// deleting the whole report is the other, blunter answer.
//
// The unread flags describe the newest turn, so only deleting that turn moves
// them. Deleting an older one leaves the conversation's last word where it was.
func (s *Store) DeleteReply(ctx context.Context, feedbackID, replyID string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx,
			`UPDATE feedback SET updated_at = updated_at WHERE id = ?`, feedbackID)
		if err != nil {
			return fmt.Errorf("feedback: lock thread: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return ErrNotFound
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

// SetStatus is the whole of what an operator can change. The report itself
// is what somebody wrote, and it stays as they wrote it.
func (s *Store) SetStatus(ctx context.Context, feedbackID string, status Status) (Feedback, error) {
	if !status.Valid() {
		return Feedback{}, ErrInvalidStatus
	}
	result, err := s.db.Exec(ctx,
		`UPDATE feedback SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().UnixMilli(), feedbackID)
	if err != nil {
		return Feedback{}, fmt.Errorf("feedback: set status: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		return Feedback{}, ErrNotFound
	}
	return s.ByID(ctx, nil, feedbackID)
}

func (s *Store) Delete(ctx context.Context, feedbackID string) error {
	result, err := s.db.Exec(ctx, `DELETE FROM feedback WHERE id = ?`, feedbackID)
	if err != nil {
		return fmt.Errorf("feedback: delete: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Summary is the count block above the operator's list: how much there is,
// and how much of it is still waiting.
type Summary struct {
	Total    int `json:"total"`
	Open     int `json:"open"`
	Bugs     int `json:"bugs"`
	Ideas    int `json:"ideas"`
	HighOpen int `json:"high_open"`
	// Threads whose last word is the reader's. The one number on this screen
	// that is about the operator rather than about the instance.
	Awaiting int `json:"awaiting"`
}

// Counts answers the summary in one round trip. SUM(CASE …) rather than
// COUNT(…) FILTER: both engines here understand the first spelling, and the
// second is a newer standard than the oldest SQLite this is expected to run
// against. COALESCE because SUM over no rows is NULL on both of them, and a
// fresh instance has no rows.
func (s *Store) Counts(ctx context.Context, q database.Queryer) (Summary, error) {
	var out Summary
	err := s.pick(q).QueryRow(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? AND priority = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN operator_unread = ? THEN 1 ELSE 0 END), 0)
		FROM feedback`,
		string(StatusOpen), string(KindBug), string(KindIdea),
		string(StatusOpen), string(PriorityHigh), true).
		Scan(&out.Total, &out.Open, &out.Bugs, &out.Ideas, &out.HighOpen, &out.Awaiting)
	if err != nil {
		return Summary{}, fmt.Errorf("feedback: counts: %w", err)
	}
	return out, nil
}
