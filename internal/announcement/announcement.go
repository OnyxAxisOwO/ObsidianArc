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
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// How an announcement asks for attention.
type DisplayMode string

const (
	// Pops up on every visit. For something that stays true and stays
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

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const columns = `a.id, a.title, a.body, a.display_mode, a.dismiss_after_seconds,
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
	if len(in.Body) > MaxBodyChars {
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
	return record, nil
}

func (s *Store) Update(ctx context.Context, announcementID string, in Input) (Announcement, error) {
	in, err := validate(in)
	if err != nil {
		return Announcement{}, err
	}

	now := time.Now().UnixMilli()
	result, err := s.db.Exec(ctx, `UPDATE announcements SET
		title = ?, body = ?, display_mode = ?, dismiss_after_seconds = ?,
		published = ?, pinned = ?, updated_at = ?
		WHERE id = ?`,
		in.Title, in.Body, in.DisplayMode, in.DismissAfterSeconds,
		in.Published, in.Pinned, now, announcementID)
	if err != nil {
		return Announcement{}, fmt.Errorf("announcement: update: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Announcement{}, ErrNotFound
	}
	return s.ByID(ctx, announcementID)
}

func (s *Store) Delete(ctx context.Context, announcementID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM announcements WHERE id = ?`, announcementID); err != nil {
		return fmt.Errorf("announcement: delete: %w", err)
	}
	return nil
}

func (s *Store) ByID(ctx context.Context, announcementID string) (Announcement, error) {
	return scan(s.db.QueryRow(ctx,
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
// whether this reader has opened it.
func (s *Store) ListFor(ctx context.Context, userID string) ([]Announcement, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+columns+`,
		 CASE WHEN r.user_id IS NULL THEN ? ELSE ? END
		 FROM announcements a
		 LEFT JOIN announcement_reads r ON r.announcement_id = a.id AND r.user_id = ?
		 WHERE a.published = ?`+ordering,
		false, true, userID, true)
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
func (s *Store) MarkRead(ctx context.Context, userID, announcementID string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO announcement_reads (announcement_id, user_id, read_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (announcement_id, user_id) DO NOTHING`,
		announcementID, userID, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("announcement: mark read: %w", err)
	}
	return nil
}

// MarkAllRead clears the bell in one go.
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
