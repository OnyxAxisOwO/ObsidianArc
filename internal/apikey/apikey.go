// Package apikey is the credential a script presents instead of signing in.
//
// It is deliberately the session model with the cookie taken away: a long
// random token, stored as its SHA-256 digest, resolved in one indexed lookup.
// What it adds over a session is that the owner named it, chose when it dies,
// and can see it in a list — because unlike a cookie, a key lives in someone
// else's config file and has to be recognisable later.
//
// The token itself exists exactly once, in the response to the call that
// created it. Nothing here can give it back, which is the property that makes
// the digest worth storing rather than the value.
package apikey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Key is a row. It never carries the token: there is no field for it because
// there is nothing to put in one after the moment of creation.
type Key struct {
	ID     string `json:"id"`
	UserID string `json:"-"`
	// The opening characters of the token, so the owner can tell which row is
	// the key in a given config file.
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	// Epoch millis; zero means it never expires.
	ExpiresAt  int64 `json:"expires_at"`
	LastUsedAt int64 `json:"last_used_at"`
	CreatedAt  int64 `json:"created_at"`
	UpdatedAt  int64 `json:"updated_at"`
}

// Expired reports whether the key is past its expiry at t.
func (k Key) Expired(t time.Time) bool {
	return k.ExpiresAt > 0 && k.ExpiresAt <= t.UnixMilli()
}

var (
	ErrNotFound    = errors.New("apikey: not found")
	ErrInvalidName = errors.New("apikey: name must be 1-60 characters")
	ErrPastExpiry  = errors.New("apikey: expiry is in the past")
	ErrTooMany     = errors.New("apikey: this account already has the maximum number of keys")
)

const (
	MaxNameChars = 60
	// A ceiling per account. Not a security control — the keys belong to one
	// user and spend that user's allowance either way — but a list nobody can
	// read is not a list, and an unbounded one is a way to make the table
	// grow without limit.
	MaxPerUser = 20

	// Entropy in a token. The same 256 bits a session cookie carries, and for
	// the same reason: it is the whole secret.
	tokenBytes = 32
	// Marks our tokens in a log or a leaked config while staying inside the
	// "sk-" shape that several OpenAI clients insist on before they will send
	// a request at all.
	tokenPrefix = "sk-oa-"
	// How much of the token the owner is shown afterwards. Enough to
	// recognise, far short of guessable.
	prefixChars = len(tokenPrefix) + 6
)

const columns = `id, user_id, prefix, name, expires_at, last_used_at, created_at, updated_at`

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

// Issue creates a key and returns it together with the token, which the
// caller must hand to the user immediately: this is the only time it exists.
func (s *Store) Issue(ctx context.Context, userID, name string, expiresAt int64) (Key, string, error) {
	clean, err := checkName(name)
	if err != nil {
		return Key{}, "", err
	}
	now := time.Now()
	if expiresAt < 0 || (expiresAt > 0 && expiresAt <= now.UnixMilli()) {
		return Key{}, "", ErrPastExpiry
	}

	count, err := s.count(ctx, userID)
	if err != nil {
		return Key{}, "", err
	}
	if count >= MaxPerUser {
		return Key{}, "", ErrTooMany
	}

	token := tokenPrefix + id.Secret(tokenBytes)
	record := Key{
		ID:        id.New(),
		UserID:    userID,
		Prefix:    token[:prefixChars],
		Name:      clean,
		ExpiresAt: expiresAt,
		CreatedAt: now.UnixMilli(),
		UpdatedAt: now.UnixMilli(),
	}

	_, err = s.db.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, token_hash, prefix, name, expires_at, last_used_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.UserID, Digest(token), record.Prefix, record.Name,
		record.ExpiresAt, record.LastUsedAt, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return Key{}, "", fmt.Errorf("apikey: issue: %w", err)
	}
	return record, token, nil
}

// Digest maps a presented token to its row key.
func Digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Resolve looks a presented token up. An expired key is reported as absent:
// the caller has no use for the difference, and neither does the client.
//
// It does not touch last_used_at. Recording that on the read path would turn
// every API request into a write; Touch is called separately, and only when
// the request got as far as being served.
func (s *Store) Resolve(ctx context.Context, token string) (Key, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Key{}, ErrNotFound
	}
	record, err := scan(s.db.QueryRow(ctx,
		`SELECT `+columns+` FROM api_keys WHERE token_hash = ?`, Digest(token)))
	if err != nil {
		return Key{}, err
	}
	if record.Expired(time.Now()) {
		return Key{}, ErrNotFound
	}
	return record, nil
}

// Touch records that a key was used. Best-effort by design: the caller
// ignores the error, because failing a served request to record a timestamp
// would trade something that matters for something that does not.
//
// The write is skipped unless the stored value is more than a minute old, so
// a client polling in a loop does not turn into one UPDATE per request.
func (s *Store) Touch(ctx context.Context, keyID string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ? AND last_used_at < ?`,
		now, keyID, now-time.Minute.Milliseconds())
	if err != nil {
		return fmt.Errorf("apikey: touch: %w", err)
	}
	return nil
}

// List returns one account's keys, newest first. The ids are ULIDs, so
// ordering by id is ordering by creation time without a second column.
func (s *Store) List(ctx context.Context, userID string) ([]Key, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+columns+` FROM api_keys WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("apikey: list: %w", err)
	}
	defer rows.Close()

	out := []Key{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("apikey: list: %w", err)
	}
	return out, nil
}

// Update is what the owner may change after the fact: what the key is called
// and when it stops working. Not who it belongs to, and not the token.
type Update struct {
	Name      *string
	ExpiresAt *int64
}

// Rename and re-expire, scoped to the owner.
//
// The user id is part of the WHERE clause rather than checked beforehand:
// that is what makes guessing another account's key id useless instead of
// merely unlikely.
func (s *Store) Update(ctx context.Context, userID, keyID string, in Update) (Key, error) {
	sets := []string{}
	args := []any{}

	if in.Name != nil {
		clean, err := checkName(*in.Name)
		if err != nil {
			return Key{}, err
		}
		sets = append(sets, "name = ?")
		args = append(args, clean)
	}
	if in.ExpiresAt != nil {
		expiry := *in.ExpiresAt
		// Moving the expiry into the past is how a key is retired without
		// deleting the row, so only a negative value is meaningless.
		if expiry < 0 {
			return Key{}, ErrPastExpiry
		}
		sets = append(sets, "expires_at = ?")
		args = append(args, expiry)
	}
	if len(sets) == 0 {
		return s.byID(ctx, userID, keyID)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UnixMilli(), keyID, userID)

	result, err := s.db.Exec(ctx,
		`UPDATE api_keys SET `+strings.Join(sets, ", ")+` WHERE id = ? AND user_id = ?`, args...)
	if err != nil {
		return Key{}, fmt.Errorf("apikey: update: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return Key{}, ErrNotFound
	}
	return s.byID(ctx, userID, keyID)
}

// Delete revokes a key by removing the row.
//
// There is nothing to keep: the usage ledger already records what was spent,
// and a revoked row would only be a digest nobody can present.
func (s *Store) Delete(ctx context.Context, userID, keyID string) error {
	result, err := s.db.Exec(ctx, `DELETE FROM api_keys WHERE id = ? AND user_id = ?`, keyID, userID)
	if err != nil {
		return fmt.Errorf("apikey: delete: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) byID(ctx context.Context, userID, keyID string) (Key, error) {
	return scan(s.db.QueryRow(ctx,
		`SELECT `+columns+` FROM api_keys WHERE id = ? AND user_id = ?`, keyID, userID))
}

func (s *Store) count(ctx context.Context, userID string) (int, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM api_keys WHERE user_id = ?`, userID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("apikey: count: %w", err)
	}
	return total, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner) (Key, error) {
	var record Key
	err := row.Scan(&record.ID, &record.UserID, &record.Prefix, &record.Name,
		&record.ExpiresAt, &record.LastUsedAt, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Key{}, ErrNotFound
		}
		return Key{}, fmt.Errorf("apikey: scan: %w", err)
	}
	return record, nil
}

func checkName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	length := utf8.RuneCountInString(trimmed)
	if length == 0 || length > MaxNameChars {
		return "", ErrInvalidName
	}
	return trimmed, nil
}
