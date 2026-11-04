package feedback

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
)

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

type rowScanner = database.RowScanner

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

// Create records one report for userID.
//
// The daily cap and the insert share a transaction that locks the author's
// row first, for the reason apikey.Store.IssueModels and project.Store.Create do
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
// The newest listedNewest, plus every report still holding an answer the author has
// not opened. The account-menu dot counts all of those, and the only way to
// open one is to pick it from this list, so one older than the fifty would
// keep the dot lit with no way to clear it.
func (s *Store) ListFor(ctx context.Context, q database.Queryer, userID string) ([]Feedback, error) {
	rows, err := s.pick(q).Query(ctx,
		`SELECT `+columns+` FROM feedback f
		 WHERE f.user_id = ? AND (f.author_unread = ? OR f.id IN (
		   SELECT id FROM feedback WHERE user_id = ? ORDER BY created_at DESC LIMIT ?))
		 ORDER BY f.created_at DESC`,
		userID, true, userID, listedNewest)
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
