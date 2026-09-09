package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Session is a row in the sessions table. Its ID is the SHA-256 of the cookie
// value, never the value itself: whoever reads a database dump learns which
// sessions exist and when they expire, but cannot present one.
//
// A hash is enough because the token is 256 bits of randomness, not a
// password — there is nothing to brute-force offline and therefore no reason
// to pay for a slow KDF on every authenticated request.
type Session struct {
	ID         string
	UserID     string
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
	IP         string
	UserAgent  string
}

var ErrSessionNotFound = errors.New("auth: session not found")

// TokenBytes is the entropy in a session cookie.
const TokenBytes = 32

// MaxUserAgentChars bounds what is stored from a client-supplied header.
const MaxUserAgentChars = 200

type SessionStore struct{ db *database.DB }

func NewSessionStore(db *database.DB) *SessionStore { return &SessionStore{db: db} }

// HashToken maps a cookie value to its row key.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create issues a session and returns the token to put in the cookie. The
// token is returned once and never stored, so it exists only in the caller's
// hand and in the browser.
func (s *SessionStore) Create(ctx context.Context, userID string, ttl time.Duration, ip, userAgent string) (string, Session, error) {
	token := id.Secret(TokenBytes)
	now := time.Now()

	record := Session{
		ID:         HashToken(token),
		UserID:     userID,
		CreatedAt:  now.UnixMilli(),
		ExpiresAt:  now.Add(ttl).UnixMilli(),
		LastSeenAt: now.UnixMilli(),
		IP:         ip,
		UserAgent:  text.Truncate(userAgent, MaxUserAgentChars),
	}

	_, err := s.db.Exec(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.UserID, record.CreatedAt, record.ExpiresAt,
		record.LastSeenAt, record.IP, record.UserAgent)
	if err != nil {
		return "", Session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return token, record, nil
}

// GetWithUser resolves a cookie to its session and the account that owns it.
//
// One query, because every authenticated request goes through here and this
// used to be two in sequence — the session, then the account — which is two
// round trips per request against Postgres for what one join answers. An
// inner join is sound because sessions.user_id cascades on delete and foreign
// keys are enforced on both engines, so a session whose account is gone does
// not exist to be found.
//
// An expired row is treated as absent and deleted, so the table does not
// accumulate dead sessions between janitor runs on a busy instance.
func (s *SessionStore) GetWithUser(ctx context.Context, token string) (Session, user.User, error) {
	key := HashToken(token)

	var record Session
	account, err := user.ScanRow(scanBoth{s.db.QueryRow(ctx,
		`SELECT s.id, s.user_id, s.created_at, s.expires_at, s.last_seen_at, s.ip, s.user_agent, `+
			user.JoinColumns("u")+
			` FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?`, key),
		[]any{&record.ID, &record.UserID, &record.CreatedAt, &record.ExpiresAt,
			&record.LastSeenAt, &record.IP, &record.UserAgent}})
	if err != nil {
		if database.IsNotFound(err) || errors.Is(err, user.ErrNotFound) {
			return Session{}, user.User{}, ErrSessionNotFound
		}
		return Session{}, user.User{}, fmt.Errorf("auth: load session: %w", err)
	}

	if record.ExpiresAt <= time.Now().UnixMilli() {
		_ = s.DeleteByID(ctx, key)
		return Session{}, user.User{}, ErrSessionNotFound
	}
	return record, account, nil
}

// scanBoth lets one row fill two structs: the session's columns are consumed
// here, and the rest are handed to the user package's own scanner, which is
// what keeps its column list and its scan list from drifting apart.
type scanBoth struct {
	row    interface{ Scan(dest ...any) error }
	prefix []any
}

func (s scanBoth) Scan(dest ...any) error {
	return s.row.Scan(append(append([]any{}, s.prefix...), dest...)...)
}

// Touch records that a session is still in use, and extends it. Called at
// most once per TouchInterval per session so a read-heavy workload does not
// turn into a write-heavy one.
func (s *SessionStore) Touch(ctx context.Context, sessionID string, ttl time.Duration) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		now.UnixMilli(), now.Add(ttl).UnixMilli(), sessionID)
	if err != nil {
		return fmt.Errorf("auth: touch session: %w", err)
	}
	return nil
}

func (s *SessionStore) DeleteByID(ctx context.Context, sessionID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

func (s *SessionStore) DeleteByToken(ctx context.Context, token string) error {
	return s.DeleteByID(ctx, HashToken(token))
}

// DeleteByUser signs an account out everywhere. Used when an administrator
// disables a user, and when a password changes — a stolen session must not
// outlive the credential it was issued against.
func (s *SessionStore) DeleteByUser(ctx context.Context, q database.Queryer, userID string) error {
	if q == nil {
		q = s.db
	}
	if _, err := q.Exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("auth: delete user sessions: %w", err)
	}
	return nil
}

// DeleteExpired is the janitor's whole job for this table.
func (s *SessionStore) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("auth: prune sessions: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return removed, nil
}
