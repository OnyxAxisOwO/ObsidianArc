// Package notify is the bell in the top-right corner: what happened to an
// account, or to the instance, while nobody was watching for it.
//
// A row carries a kind and a bag of params, never a sentence — "the client
// words it in the reader's language from kind and params" is the one rule
// that keeps a notice translatable without a migration and keeps this package
// out of the business of composing English or Chinese. See web/src/i18n.ts
// for the wording each kind resolves to.
//
// Visibility is computed from what a row says about itself rather than
// written out once per recipient: 'user' reaches one account, 'all' reaches
// everyone who existed when it was created, and 'admins' reaches every
// administrator or — when permission is set — only the ones holding that
// grant. A fan-out table would need a write per recipient and a delete per
// removal; this needs neither.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Audience is who a notification reaches.
type Audience string

const (
	AudienceUser   Audience = "user"
	AudienceAll    Audience = "all"
	AudienceAdmins Audience = "admins"
)

func (a Audience) valid() bool {
	switch a {
	case AudienceUser, AudienceAll, AudienceAdmins:
		return true
	default:
		return false
	}
}

var (
	ErrInvalidAudience   = errors.New("notify: audience must be user, all or admins")
	ErrUserRequired      = errors.New("notify: audience \"user\" requires a user id")
	ErrInvalidPermission = errors.New("notify: permission must be empty or a known admin grant")
	ErrKindRequired      = errors.New("notify: kind is required")
)

// Notification is one row in the bell.
//
// Audience, UserID and Permission decide who is shown it, and are never sent
// to the browser: a reader who can see a row already knows why, and the
// three fields would just be internal plumbing on the wire. Kind, Params and
// Link are the whole of what the client needs to word it and send a click
// somewhere.
type Notification struct {
	ID         string         `json:"id"`
	Audience   Audience       `json:"-"`
	UserID     string         `json:"-"`
	Permission string         `json:"-"`
	Kind       string         `json:"kind"`
	Params     map[string]any `json:"params"`
	Link       string         `json:"link"`
	CreatedAt  int64          `json:"created_at"`
	// What single source this notice is about — an announcement's id, say —
	// so Retract can find every notice that one produced. Empty for every
	// kind with nothing to retract. Never sent to the browser: it exists for
	// the reverse lookup a retraction needs, not for anything a reader does
	// with it.
	Ref string `json:"-"`
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) pick(q database.Queryer) database.Queryer {
	if q == nil {
		return s.db
	}
	return q
}

// Push records one notification. q is the transaction sharing the write that
// caused it when there is one — a reply, a grant, a role change — so the
// notice and the change it describes commit or roll back together; nil
// writes standalone for the callers with nothing to join, which push
// detached with a short timeout of their own rather than fail the request
// that triggered them.
func (s *Store) Push(ctx context.Context, q database.Queryer, n Notification) error {
	if !n.Audience.valid() {
		return ErrInvalidAudience
	}
	if n.Audience == AudienceUser {
		if n.UserID == "" {
			return ErrUserRequired
		}
	} else {
		// Never persisted for the other two audiences, whatever a caller left
		// set on the struct — the column means something only for 'user'.
		n.UserID = ""
	}
	if n.Permission != "" && !user.ValidPermission(n.Permission) {
		return ErrInvalidPermission
	}
	if strings.TrimSpace(n.Kind) == "" {
		return ErrKindRequired
	}

	if n.CreatedAt == 0 {
		n.CreatedAt = time.Now().UnixMilli()
	}
	if n.ID == "" {
		n.ID = id.NewAt(time.UnixMilli(n.CreatedAt))
	}
	params := n.Params
	if params == nil {
		params = map[string]any{}
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("notify: encode params: %w", err)
	}

	_, err = s.pick(q).Exec(ctx, `INSERT INTO notifications
		(id, audience, user_id, permission, kind, params, link, created_at, ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, string(n.Audience), nullable(n.UserID), n.Permission, n.Kind, string(encoded), n.Link, n.CreatedAt, n.Ref)
	if err != nil {
		return fmt.Errorf("notify: push: %w", err)
	}
	return nil
}

// Retract removes every notice of this kind pointing at ref — an
// announcement retracted or deleted, say, whose headline must not go on
// showing up in every account's bell for the rest of the retention window.
// q is the transaction sharing the write that caused it when there is one,
// nil otherwise, the same convention as Push. A blank ref matches nothing:
// most kinds never set one, and refusing to run a kind-only delete is what
// keeps a caller's mistake from wiping every notice of that kind ever sent.
func (s *Store) Retract(ctx context.Context, q database.Queryer, kind, ref string) (int64, error) {
	if ref == "" {
		return 0, nil
	}
	result, err := s.pick(q).Exec(ctx, `DELETE FROM notifications WHERE kind = ? AND ref = ?`, kind, ref)
	if err != nil {
		return 0, fmt.Errorf("notify: retract: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("notify: retract rows affected: %w", err)
	}
	return affected, nil
}

// visibility builds the WHERE fragment (without the leading "WHERE") and its
// bound arguments for what account may see. Shared by List, Poll and Unread
// so the three can never quietly disagree about who a row reaches.
func visibility(account user.User) (string, []any) {
	conditions := []string{"(audience = 'user' AND user_id = ?)"}
	args := []any{account.ID}

	// created_at rather than a join to signup: an account cannot be surprised
	// by an announcement made before it existed, and this is the one column
	// every row already carries.
	conditions = append(conditions, "(audience = 'all' AND created_at >= ?)")
	args = append(args, account.CreatedAt)

	if account.IsAdmin() {
		if account.IsSuperAdmin() {
			// A super admin holds every grant, so the permission column
			// cannot narrow anything for them — see user.CanAdmin.
			conditions = append(conditions, "audience = 'admins'")
		} else {
			permConds := []string{"permission = ''"}
			for _, permission := range account.AdminPermissions {
				if user.ValidPermission(permission) {
					permConds = append(permConds, "permission = ?")
					args = append(args, permission)
				}
			}
			conditions = append(conditions, "(audience = 'admins' AND ("+strings.Join(permConds, " OR ")+"))")
		}
	}
	return "(" + strings.Join(conditions, " OR ") + ")", args
}

// List is the bell's own feed: newest first, optionally continued from
// before a moment already shown. limit is clamped the way every other list
// in this project is, so a malformed value cannot ask for the whole table.
func (s *Store) List(ctx context.Context, account user.User, limit int, before int64) ([]Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	where, args := visibility(account)
	query := `SELECT id, kind, params, link, created_at FROM notifications WHERE ` + where
	if before > 0 {
		query += ` AND created_at < ?`
		args = append(args, before)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("notify: list: %w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

// The slack a poll asks with, and the most rows one answer hands back.
const (
	// created_at is stamped when a row is built, not when its transaction
	// commits, so a row can be stamped a few milliseconds before one the
	// client has already seen and still commit after it. An exact "> after"
	// would skip that row forever once the cursor moves past its timestamp;
	// re-asking the last ten seconds catches it, at the cost of re-sending
	// rows the client has already seen — which is exactly why the client
	// de-dupes by id instead of trusting created_at alone.
	pollGraceMillis = 10_000
	pollLimit       = 200
)

// Poll is what a signed-in client asks every thirty seconds and on regaining
// focus: whatever this account can see that is newer than the last thing it
// was shown, plus the grace window above. Ascending by created_at, and by id
// where two rows share a millisecond, because a caller raising one toast per
// row wants to raise them in the order they happened, and a page has to be
// cut at a reproducible point to be a page at all.
func (s *Store) Poll(ctx context.Context, account user.User, after int64) ([]Notification, error) {
	where, args := visibility(account)
	args = append(args, after-pollGraceMillis, pollLimit)
	rows, err := s.db.Query(ctx,
		`SELECT id, kind, params, link, created_at FROM notifications
		 WHERE `+where+` AND created_at > ? ORDER BY created_at ASC, id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("notify: poll: %w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

// Unread counts what this account can see that arrived after the moment
// named — its own read watermark for the bell's badge, or a client's last
// poll for a cheaper "is there anything at all". Announcements are excluded:
// that feed keeps its own read state per account (see package announcement,
// AnnounceBell.vue), so counting an announcement here too would have this
// badge and that one both claiming the same notice as unread.
func (s *Store) Unread(ctx context.Context, account user.User, after int64) (int, error) {
	where, args := visibility(account)
	args = append(args, after, "announcement")
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE `+where+` AND created_at > ? AND kind <> ?`, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("notify: unread: %w", err)
	}
	return count, nil
}

// SeenAt is the watermark MarkRead last wrote, or zero for an account that
// has never cleared the bell — which correctly counts everything as unread.
func (s *Store) SeenAt(ctx context.Context, userID string) (int64, error) {
	var seenAt int64
	err := s.db.QueryRow(ctx,
		`SELECT seen_at FROM notification_reads WHERE user_id = ?`, userID).Scan(&seenAt)
	if database.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("notify: seen at: %w", err)
	}
	return seenAt, nil
}

// MarkRead moves this account's watermark forward to until, clamped to the
// server's own clock: zero, negative, or ahead of now all become now. A
// client's up_to can move the watermark forward, never past what has
// actually happened yet — a client clock running fast must not be able to
// mark as read a notice that has not been pushed. One upsert rather than a
// read then a write: the larger of the two values is chosen by the statement
// itself, so two tabs clearing the bell at the same moment cannot walk the
// watermark backwards — the check-then-write race a mutex would not catch
// across two instances.
func (s *Store) MarkRead(ctx context.Context, userID string, until int64) error {
	if now := time.Now().UnixMilli(); until <= 0 || until > now {
		until = now
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO notification_reads (user_id, seen_at) VALUES (?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET
		   seen_at = CASE WHEN excluded.seen_at > notification_reads.seen_at
		                  THEN excluded.seen_at ELSE notification_reads.seen_at END`,
		userID, until)
	if err != nil {
		return fmt.Errorf("notify: mark read: %w", err)
	}
	return nil
}

// Prune drops rows nothing will read again. Called from the janitor's sweep
// with a cutoff around thirty days back. The read watermark is left alone:
// it is a handful of bytes per account, correct forever whether or not the
// notice it once counted still exists.
func (s *Store) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM notifications WHERE created_at < ?`, olderThan.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("notify: prune: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("notify: prune rows affected: %w", err)
	}
	return affected, nil
}

type rowsScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanAll(rows rowsScanner) ([]Notification, error) {
	out := []Notification{}
	for rows.Next() {
		var (
			record Notification
			params string
		)
		if err := rows.Scan(&record.ID, &record.Kind, &params, &record.Link, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("notify: scan: %w", err)
		}
		if err := json.Unmarshal([]byte(params), &record.Params); err != nil {
			return nil, fmt.Errorf("notify: decode params: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// An empty user id is NULL rather than "", matching every other optional
// reference in this schema — nullable() in internal/user does the same.
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
