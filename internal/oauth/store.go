package oauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

var (
	// No row for this provider and subject. The ordinary answer for somebody
	// signing in with a provider for the first time.
	ErrNoIdentity = errors.New("oauth: no account is connected to that identity")
	// The row is already there — the same provider account on another
	// account here, or this account already holds a connection to this
	// provider.
	ErrAlreadyLinked = errors.New("oauth: that identity is already connected")
)

// Connection is one linked provider account, as its owner's settings screen
// reads it. The subject is deliberately absent: it identifies the person to
// the provider and says nothing to the person themselves.
type Connection struct {
	Provider    string `json:"provider"`
	Login       string `json:"login"`
	Email       string `json:"email"`
	CreatedAt   int64  `json:"created_at"`
	LastLoginAt int64  `json:"last_login_at"`
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) queryer(q database.Queryer) database.Queryer {
	if q == nil {
		return s.db
	}
	return q
}

// Account resolves a provider's own identifier to an account here.
func (s *Store) Account(ctx context.Context, q database.Queryer, provider, subject string) (string, error) {
	var userID string
	err := s.queryer(q).QueryRow(ctx,
		`SELECT user_id FROM oauth_identities WHERE provider = ? AND subject = ?`,
		provider, subject).Scan(&userID)
	if err != nil {
		if database.IsNotFound(err) {
			return "", ErrNoIdentity
		}
		return "", fmt.Errorf("oauth: read identity: %w", err)
	}
	return userID, nil
}

// Link records that a provider account belongs to an account here.
//
// Both unique indexes are load-bearing and both are reported as the same
// refusal: one subject is one account, and one account holds one connection
// per provider. The check that precedes this in the service is about wording
// the answer, not about correctness — the index is what makes it true when
// two callbacks arrive at once.
func (s *Store) Link(ctx context.Context, q database.Queryer, userID string, identity Identity) error {
	now := time.Now().UnixMilli()
	_, err := s.queryer(q).Exec(ctx,
		`INSERT INTO oauth_identities
		 (id, provider, subject, user_id, email, login, created_at, last_login_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id.New(), identity.Provider, identity.Subject, userID,
		identity.Email, identity.Login, now, now)
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "unique") || strings.Contains(message, "duplicate") {
			return ErrAlreadyLinked
		}
		return fmt.Errorf("oauth: link identity: %w", err)
	}
	return nil
}

// Touch records a sign-in, and keeps the name and address the connections
// screen shows in step with what the provider says now.
func (s *Store) Touch(ctx context.Context, q database.Queryer, identity Identity) error {
	_, err := s.queryer(q).Exec(ctx,
		`UPDATE oauth_identities SET email = ?, login = ?, last_login_at = ?
		 WHERE provider = ? AND subject = ?`,
		identity.Email, identity.Login, time.Now().UnixMilli(),
		identity.Provider, identity.Subject)
	if err != nil {
		return fmt.Errorf("oauth: record sign-in: %w", err)
	}
	return nil
}

// For lists one account's connections, oldest first.
func (s *Store) For(ctx context.Context, q database.Queryer, userID string) ([]Connection, error) {
	rows, err := s.queryer(q).Query(ctx,
		`SELECT provider, login, email, created_at, last_login_at
		 FROM oauth_identities WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("oauth: list connections: %w", err)
	}
	defer rows.Close()

	out := []Connection{}
	for rows.Next() {
		var item Connection
		if err := rows.Scan(&item.Provider, &item.Login, &item.Email,
			&item.CreatedAt, &item.LastLoginAt); err != nil {
			return nil, fmt.Errorf("oauth: scan connection: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oauth: list connections: %w", err)
	}
	return out, nil
}

// Count is how many ways into this account a provider currently offers. Read
// by the unlink check, which is why it takes a Queryer: it has to run inside
// the transaction that holds the account's row lock.
func (s *Store) Count(ctx context.Context, q database.Queryer, userID string) (int, error) {
	var count int
	if err := s.queryer(q).QueryRow(ctx,
		`SELECT COUNT(*) FROM oauth_identities WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("oauth: count connections: %w", err)
	}
	return count, nil
}

// HasProvider reports whether an account already holds a connection to one
// named provider. Used by the OIDC binding gate, which cares about exactly
// one provider rather than the count Count answers for the unlink check.
func (s *Store) HasProvider(ctx context.Context, q database.Queryer, userID, provider string) (bool, error) {
	var count int
	if err := s.queryer(q).QueryRow(ctx,
		`SELECT COUNT(*) FROM oauth_identities WHERE user_id = ? AND provider = ?`,
		userID, provider).Scan(&count); err != nil {
		return false, fmt.Errorf("oauth: check provider connection: %w", err)
	}
	return count > 0, nil
}

// Unlink removes one connection. The boolean is whether there was one, so a
// second click on a stale screen is reported rather than silently succeeding.
func (s *Store) Unlink(ctx context.Context, q database.Queryer, userID, provider string) (bool, error) {
	result, err := s.queryer(q).Exec(ctx,
		`DELETE FROM oauth_identities WHERE user_id = ? AND provider = ?`, userID, provider)
	if err != nil {
		return false, fmt.Errorf("oauth: unlink: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("oauth: unlink: %w", err)
	}
	return affected > 0, nil
}

// SpendState records that a sign-in state has come back, and reports whether
// this call was the first to say so.
//
// The insert is the check: the nonce is the primary key, so a second arrival
// conflicts and changes nothing, on both engines. A read followed by a write
// would let two callbacks racing with one state both see it unspent.
func (s *Store) SpendState(ctx context.Context, q database.Queryer, nonce string, expires int64) (bool, error) {
	result, err := s.queryer(q).Exec(ctx,
		`INSERT INTO oauth_states (nonce, expires_at) VALUES (?, ?)
		 ON CONFLICT (nonce) DO NOTHING`,
		nonce, expires)
	if err != nil {
		return false, fmt.Errorf("oauth: spend state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("oauth: spend state: %w", err)
	}
	return affected == 1, nil
}

// PruneStates drops the spent states whose expiry has passed. Nothing reads
// them again, since the callback refuses a state past its expiry before it
// asks whether the state was spent.
func (s *Store) PruneStates(ctx context.Context, now int64) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM oauth_states WHERE expires_at < ?`, now)
	if err != nil {
		return 0, fmt.Errorf("oauth: prune states: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("oauth: prune states: %w", err)
	}
	return affected, nil
}
