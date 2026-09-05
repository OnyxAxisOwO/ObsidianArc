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
		UserAgent:  truncate(userAgent, MaxUserAgentChars),
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

// Get resolves a cookie value. An expired row is treated as absent and
// deleted, so the table does not accumulate dead sessions between janitor
// runs on a busy instance.
func (s *SessionStore) Get(ctx context.Context, token string) (Session, error) {
	key := HashToken(token)

	var record Session
	err := s.db.QueryRow(ctx,
		`SELECT id, user_id, created_at, expires_at, last_seen_at, ip, user_agent
		 FROM sessions WHERE id = ?`, key).
		Scan(&record.ID, &record.UserID, &record.CreatedAt, &record.ExpiresAt,
			&record.LastSeenAt, &record.IP, &record.UserAgent)
	if err != nil {
		if database.IsNotFound(err) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, fmt.Errorf("auth: load session: %w", err)
	}

	if record.ExpiresAt <= time.Now().UnixMilli() {
		_ = s.DeleteByID(ctx, key)
		return Session{}, ErrSessionNotFound
	}
	return record, nil
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

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
