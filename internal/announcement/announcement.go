// Package announcement is the operator's channel to everyone using the
// instance: a downtime warning, a new model, a change of terms.
//
// Rows rather than a settings blob, because they carry per-user read state
// and because "what did we tell people, and when" is worth being able to look
// up afterwards.
//
// The body is Markdown, not HTML. It is drawn into every signed-in user's
// page, and the transcript already has a renderer that puts no untrusted
// string anywhere near innerHTML; reusing it is cheaper than auditing a
// second path, and an administrator loses nothing they were going to use.
package announcement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
)

// How an announcement asks for attention.
type DisplayMode string

const (
	// Pops up on every visit, unless an unread "once" notice takes that
	// visit's popup first. For something that stays true and stays
	// important — "this instance is read-only until Friday".
	DisplayAlways DisplayMode = "always"
	// Pops up until this reader has seen it, then only lives in the list.
	DisplayOnce DisplayMode = "once"
	// Never pops up. It is in the list and it marks the bell, and that is all.
	DisplaySilent DisplayMode = "silent"
)

var Modes = []DisplayMode{DisplayAlways, DisplayOnce, DisplaySilent}

func (m DisplayMode) Valid() bool {
	for _, candidate := range Modes {
		if m == candidate {
			return true
		}
	}
	return false
}

const (
	MaxTitleChars = 120
	MaxBodyChars  = 32 * 1024
	// A dismiss delay is there to make someone read the first line, not to
	// trap them. Anything longer reads as a fault.
	MaxDismissSeconds = 60
)

var (
	ErrNotFound     = errors.New("announcement: not found")
	ErrInvalidTitle = errors.New("announcement: title must be 1-120 characters")
	ErrInvalidBody  = errors.New("announcement: body is too long")
	ErrInvalidMode  = errors.New("announcement: unknown display mode")
)

type Announcement struct {
	ID                  string      `json:"id"`
	Title               string      `json:"title"`
	Body                string      `json:"body"`
	DisplayMode         DisplayMode `json:"display_mode"`
	DismissAfterSeconds int         `json:"dismiss_after_seconds"`
	Published           bool        `json:"published"`
	Pinned              bool        `json:"pinned"`
	CreatedAt           int64       `json:"created_at"`
	UpdatedAt           int64       `json:"updated_at"`

	// Filled only for a signed-in reader's own listing.
	Read bool `json:"read"`
}

type Store struct {
	db *database.DB
	// Set by the wiring, never by NewStore: nil is what every existing test
	// and every instance that predates this feature gets, and nil means
	// "push nothing" rather than a nil-pointer panic.
	Notify *notify.Store
}

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const columns = `a.id, a.title, a.body, a.display_mode, a.dismiss_after_seconds,
	a.published, a.pinned, a.created_at, a.updated_at`

// summaryColumns is what a reader's list carries: everything but the body. The
// bell fetches the whole list on every page load, and a body can be 32 KiB, so
// the list does not read them. The popup gets its body from ByID, and a row the
// reader opens gets its body from GET /api/announcements/{id}.
const summaryColumns = `a.id, a.title, '' AS body, a.display_mode, a.dismiss_after_seconds,
	a.published, a.pinned, a.created_at, a.updated_at`

// Newest first, but anything pinned floats above it.
const ordering = ` ORDER BY a.pinned DESC, a.created_at DESC`

type Input struct {
	Title               string
	Body                string
	DisplayMode         DisplayMode
	DismissAfterSeconds int
	Published           bool
	Pinned              bool
}

func validate(in Input) (Input, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len([]rune(in.Title)) > MaxTitleChars {
		return Input{}, ErrInvalidTitle
	}
	// Counted in characters like the home notice, chat messages and feedback.
	// Bytes would admit a third as much Chinese text as the name promises.
	if utf8.RuneCountInString(in.Body) > MaxBodyChars {
		return Input{}, ErrInvalidBody
	}
	if in.DisplayMode == "" {
		in.DisplayMode = DisplayOnce
	}
	if !in.DisplayMode.Valid() {
		return Input{}, ErrInvalidMode
	}
	// Clamped rather than rejected: an operator typing 600 meant "a while",
	// and refusing the save teaches them nothing they need to know.
	if in.DismissAfterSeconds < 0 {
		in.DismissAfterSeconds = 0
	}
	if in.DismissAfterSeconds > MaxDismissSeconds {
		in.DismissAfterSeconds = MaxDismissSeconds
	}
	return in, nil
}

func (s *Store) Create(ctx context.Context, in Input) (Announcement, error) {
	in, err := validate(in)
	if err != nil {
		return Announcement{}, err
	}

	now := time.Now().UnixMilli()
	record := Announcement{
		ID:                  id.New(),
		Title:               in.Title,
		Body:                in.Body,
		DisplayMode:         in.DisplayMode,
		DismissAfterSeconds: in.DismissAfterSeconds,
		Published:           in.Published,
		Pinned:              in.Pinned,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	_, err = s.db.Exec(ctx, `INSERT INTO announcements
		(id, title, body, display_mode, dismiss_after_seconds, published, pinned, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Title, record.Body, record.DisplayMode, record.DismissAfterSeconds,
		record.Published, record.Pinned, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return Announcement{}, fmt.Errorf("announcement: create: %w", err)
	}
	if record.Published {
		s.notifyPublished(ctx, record)
	}
	return record, nil
}

func (s *Store) Update(ctx context.Context, announcementID string, in Input) (Announcement, error) {
	in, err := validate(in)
	if err != nil {
		return Announcement{}, err
	}

	// Read before the write so publishing can be told apart from an edit to
	// something already published — the notice is for "there is something
	// new to read", not for every touch-up of the wording afterwards. The read
	// and the write share a transaction behind a row lock, so two saves that
	// both publish cannot both see the draft as unpublished and both ring.
	var previous, updated Announcement
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx, `UPDATE announcements SET updated_at = updated_at WHERE id = ?`, announcementID)
		if err != nil {
			return fmt.Errorf("announcement: lock: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return ErrNotFound
		}
		if previous, err = load(ctx, tx, announcementID); err != nil {
			return err
		}

		now := time.Now().UnixMilli()
		result, err := tx.Exec(ctx, `UPDATE announcements SET
			title = ?, body = ?, display_mode = ?, dismiss_after_seconds = ?,
			published = ?, pinned = ?, updated_at = ?
			WHERE id = ?`,
			in.Title, in.Body, in.DisplayMode, in.DismissAfterSeconds,
			in.Published, in.Pinned, now, announcementID)
		if err != nil {
			return fmt.Errorf("announcement: update: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrNotFound
		}
		updated, err = load(ctx, tx, announcementID)
		return err
	})
	if err != nil {
		return Announcement{}, err
	}
	switch {
	case !previous.Published && updated.Published:
		s.notifyPublished(ctx, updated)
	case previous.Published && !updated.Published:
		// Unpublishing is a retraction: whatever the bell told every account
		// was no longer true the moment this saved, so the notice goes with
		// it rather than sitting in 30 days of inboxes for something nobody
		// can any longer click through to read.
		s.retract(ctx, announcementID)
	}
	return updated, nil
}

// notifyPublished tells every account there is something new to read.
//
// A silent announcement never asks for attention, so it gets no push: it
// reaches the list and the bell's dot through the unread count, and a push
// would be a toast the operator said should never appear.
//
// Never inside the save's transaction: by the time this runs the save has
// committed. The push runs on its own 5 second timeout, detached from the
// request's cancellation, so a client that gives up does not cancel it. The
// save does wait for it, bounded by that timeout. A failure here is logged
// rather than turning a successful publish into a failed request: the
// announcement is saved either way, and a missed bell is a smaller loss than
// losing the edit.
func (s *Store) notifyPublished(ctx context.Context, record Announcement) {
	if s.Notify == nil || record.DisplayMode == DisplaySilent {
		return
	}
	pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.Notify.Push(pushCtx, nil, notify.Notification{
		Audience: notify.AudienceAll, Kind: "announcement", Ref: record.ID,
		Params: map[string]any{"title": record.Title}, Link: "/",
	}); err != nil {
		slog.ErrorContext(ctx, "announcement: notify publish", "error", err, "id", record.ID)
		return
	}
	// The push lands after the save has committed, so a delete or an unpublish
	// that committed meanwhile may already have run its retract before this
	// push existed. Either that change commits after this check, and its own
	// retract then runs after the push and removes it, or it committed before,
	// and the check sees it here.
	current, err := s.ByID(pushCtx, record.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		slog.ErrorContext(ctx, "announcement: recheck after notify", "error", err, "id", record.ID)
		// Without knowing whether it is still published, the push is taken back:
		// an unneeded retract only removes a live bell entry, while a stale one
		// outlives an unpublish.
		s.retract(ctx, record.ID)
		return
	}
	if errors.Is(err, ErrNotFound) || !current.Published {
		s.retract(ctx, record.ID)
	}
}

// retract removes every notice this announcement's publish produced, so a
// retracted or deleted announcement stops repeating its headline in every
// account's bell for the rest of the retention window.
//
// Detached the same way notifyPublished is: it runs on its own 5 second
// timeout, so a client that gives up does not cancel it, and a failure is
// logged rather than turning a successful delete or edit into a failed request.
// The save does wait for it, bounded by that timeout.
func (s *Store) retract(ctx context.Context, announcementID string) {
	if s.Notify == nil {
		return
	}
	retractCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.Notify.Retract(retractCtx, nil, "announcement", announcementID); err != nil {
		slog.ErrorContext(ctx, "announcement: retract notices", "error", err, "id", announcementID)
	}
}

func (s *Store) Delete(ctx context.Context, announcementID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM announcements WHERE id = ?`, announcementID); err != nil {
		return fmt.Errorf("announcement: delete: %w", err)
	}
	s.retract(ctx, announcementID)
	return nil
}

func (s *Store) ByID(ctx context.Context, announcementID string) (Announcement, error) {
	return load(ctx, s.db, announcementID)
}

func load(ctx context.Context, q database.Queryer, announcementID string) (Announcement, error) {
	return scan(q.QueryRow(ctx,
		`SELECT `+columns+` FROM announcements a WHERE a.id = ?`, announcementID))
}

// ListAll is the administrator's view: drafts included.
func (s *Store) ListAll(ctx context.Context) ([]Announcement, error) {
	rows, err := s.db.Query(ctx, `SELECT `+columns+` FROM announcements a`+ordering)
	if err != nil {
		return nil, fmt.Errorf("announcement: list: %w", err)
	}
	defer rows.Close()
	return collect(rows)
}

// ListFor is what a signed-in reader sees: published rows, each carrying
// whether this reader has opened it. The bodies are left empty; see
// summaryColumns.
func (s *Store) ListFor(ctx context.Context, userID string) ([]Announcement, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+summaryColumns+`,
		 CASE WHEN r.user_id IS NULL THEN false ELSE true END
		 FROM announcements a
		 LEFT JOIN announcement_reads r ON r.announcement_id = a.id AND r.user_id = ?
		 WHERE a.published = ?`+ordering,
		userID, true)
	if err != nil {
		return nil, fmt.Errorf("announcement: list for user: %w", err)
	}
	defer rows.Close()

	out := []Announcement{}
	for rows.Next() {
		record, err := scanWithRead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// MarkRead is idempotent: opening the same announcement twice is not an
// error, and the first read is the one worth keeping.
//
// A draft is refused here, not only in the handler: a read row written for one
// would stop its popup for good once it is published. The published check and
// the insert share a transaction behind the row lock Update takes, so an
// unpublish committing in between waits for this read instead of slipping past
// it. An unknown id and a draft both return ErrNotFound.
func (s *Store) MarkRead(ctx context.Context, userID, announcementID string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx,
			`UPDATE announcements SET updated_at = updated_at WHERE id = ? AND published = ?`,
			announcementID, true)
		if err != nil {
			return fmt.Errorf("announcement: lock: %w", err)
		}
		affected, err := locked.RowsAffected()
		if err != nil {
			return fmt.Errorf("announcement: lock: %w", err)
		}
		if affected == 0 {
			return ErrNotFound
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO announcement_reads (announcement_id, user_id, read_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT (announcement_id, user_id) DO NOTHING`,
			announcementID, userID, time.Now().UnixMilli())
		if err != nil {
			return fmt.Errorf("announcement: mark read: %w", err)
		}
		return nil
	})
}

// markSeenBatch keeps each statement under PostgreSQL's limit of 65,535 bound
// parameters. It serves direct callers of the store and any list over 500 ids,
// which the handler does send. It is not set by the HTTP body cap: that cap
// alone keeps a request under the parameter limit. Each batch is idempotent,
// so a failure part way leaves nothing a retry does not finish.
const markSeenBatch = 500

// MarkSeen records that the reader has seen the announcements in the list the
// bell rendered, and nothing else. A row published after that list was
// fetched was never shown, so it stays unread and its popup still comes.
// Drafts are skipped even if their ids are sent.
func (s *Store) MarkSeen(ctx context.Context, userID string, announcementIDs []string) error {
	readAt := time.Now().UnixMilli()
	for start := 0; start < len(announcementIDs); start += markSeenBatch {
		batch := announcementIDs[start:min(start+markSeenBatch, len(announcementIDs))]
		args := []any{userID, readAt, true}
		placeholders := make([]string, 0, len(batch))
		for _, announcementID := range batch {
			placeholders = append(placeholders, "?")
			args = append(args, announcementID)
		}
		_, err := s.db.Exec(ctx,
			`INSERT INTO announcement_reads (announcement_id, user_id, read_at)
			 SELECT a.id, ?, ? FROM announcements a
			 WHERE a.published = ? AND a.id IN (`+strings.Join(placeholders, ", ")+`)
			 ON CONFLICT (announcement_id, user_id) DO NOTHING`,
			args...)
		if err != nil {
			return fmt.Errorf("announcement: mark seen: %w", err)
		}
	}
	return nil
}

// MarkAllRead marks every published row, including any the reader was never
// sent, which is why the bell no longer uses it. It stays for callers outside
// this tree; see MarkSeen.
func (s *Store) MarkAllRead(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO announcement_reads (announcement_id, user_id, read_at)
		 SELECT a.id, ?, ? FROM announcements a WHERE a.published = ?
		 ON CONFLICT (announcement_id, user_id) DO NOTHING`,
		userID, time.Now().UnixMilli(), true)
	if err != nil {
		return fmt.Errorf("announcement: mark all read: %w", err)
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner) (Announcement, error) {
	var record Announcement
	err := row.Scan(&record.ID, &record.Title, &record.Body, &record.DisplayMode,
		&record.DismissAfterSeconds, &record.Published, &record.Pinned,
		&record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Announcement{}, ErrNotFound
		}
		return Announcement{}, fmt.Errorf("announcement: scan: %w", err)
	}
	return record, nil
}

func scanWithRead(row rowScanner) (Announcement, error) {
	var record Announcement
	err := row.Scan(&record.ID, &record.Title, &record.Body, &record.DisplayMode,
		&record.DismissAfterSeconds, &record.Published, &record.Pinned,
		&record.CreatedAt, &record.UpdatedAt, &record.Read)
	if err != nil {
		return Announcement{}, fmt.Errorf("announcement: scan: %w", err)
	}
	return record, nil
}

type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
}

func collect(rows rowsScanner) ([]Announcement, error) {
	out := []Announcement{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
