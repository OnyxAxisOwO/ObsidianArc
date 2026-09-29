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
	// Password proved, code not yet. Attach does not treat a session like
	// this as signed in; only the second step reads it.
	TwoFactorPending bool
	// When this session last proved a code for the backoffice, or last used
	// the backoffice after proving one; zero when it has not, or has left.
	BackofficeAt int64
	// The address and browser that proved it, for the operator's switches
	// that end a visit when either changes.
	BackofficeIP string
	BackofficeUA string
	// The device this session was signed in from; empty for a session issued
	// before devices were recorded, which Attach fills in on its next visit.
	DeviceID string
}

var ErrSessionNotFound = errors.New("auth: session not found")

// TokenBytes is the entropy in a session cookie.
const TokenBytes = 32

// MaxUserAgentChars bounds what is stored from a client-supplied header.
const MaxUserAgentChars = user.MaxSignupUserAgentChars

type SessionStore struct {
	db *database.DB
	// The account half of the joined lookup is the user store's column
	// list, which plugins widen and narrow as they are switched.
	users *user.Store
}

func NewSessionStore(db *database.DB, users *user.Store) *SessionStore {
	return &SessionStore{db: db, users: users}
}

// HashToken maps a cookie value to its row key.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// sessionRefLength is how much of a session's id the "signed-in devices"
// screen is shown and asked to send back — enough of a 256-bit hash that a
// caller cannot feasibly guess another session's, short enough to read as an
// id rather than the row key it actually is.
const sessionRefLength = 16

// ValidSessionRef reports whether s could be one of the opaque ids this
// package hands out for a session — never the full stored id, and never the
// token. Guards a path parameter before it reaches DeleteByPrefixForUser's
// LIKE query, the way id.Valid guards a ULID before a lookup.
func ValidSessionRef(s string) bool {
	if len(s) != sessionRefLength {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// sessionRef is the opaque id a session is shown as: HashToken's own hex
// alphabet, so ValidSessionRef's check on the way back in is exact rather
// than a guess at what Go's hex encoding happens to produce.
func sessionRef(id string) string {
	// No session at all — a request dispatched from the SSH console carries
	// none — is no ref, not a slice past the end of an empty string.
	if len(id) < sessionRefLength {
		return ""
	}
	return id[:sessionRefLength]
}

// Create issues a session and returns the token to put in the cookie. The
// token is returned once and never stored, so it exists only in the caller's
// hand and in the browser.
func (s *SessionStore) Create(ctx context.Context, userID string, ttl time.Duration, ip, userAgent string) (string, Session, error) {
	return s.create(ctx, userID, ttl, ip, userAgent, false)
}

// CreatePending issues the half of a session a password buys when the
// account also asks for a code. It is a row like any other so the cookie,
// the expiry and sign-out all work the way they already do; the flag is what
// keeps it from being mistaken for the whole.
func (s *SessionStore) CreatePending(ctx context.Context, userID string, ttl time.Duration, ip, userAgent string) (string, Session, error) {
	return s.create(ctx, userID, ttl, ip, userAgent, true)
}

func (s *SessionStore) create(ctx context.Context, userID string, ttl time.Duration, ip, userAgent string, pending bool) (string, Session, error) {
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

		TwoFactorPending: pending,
	}

	_, err := s.db.Exec(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at, ip, user_agent, two_factor_pending)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.UserID, record.CreatedAt, record.ExpiresAt,
		record.LastSeenAt, record.IP, record.UserAgent, record.TwoFactorPending)
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
	account, err := s.users.ScanRow(scanBoth{s.db.QueryRow(ctx,
		`SELECT s.id, s.user_id, s.created_at, s.expires_at, s.last_seen_at, s.ip, s.user_agent,
		 s.two_factor_pending, s.backoffice_at, s.backoffice_ip, s.backoffice_ua, s.device_id, `+
			s.users.JoinColumns("u")+
			` FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?`, key),
		[]any{&record.ID, &record.UserID, &record.CreatedAt, &record.ExpiresAt,
			&record.LastSeenAt, &record.IP, &record.UserAgent, &record.TwoFactorPending,
			&record.BackofficeAt, &record.BackofficeIP, &record.BackofficeUA, &record.DeviceID}})
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
	account, err = user.NewStore(s.db).ResolveMembership(ctx, nil, account)
	return record, account, err
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

// EnterBackofficeVisit records a proved visit and where it was proved from.
func (s *SessionStore) EnterBackofficeVisit(ctx context.Context, sessionID string, at int64, ip, userAgent string) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE sessions SET backoffice_at = ?, backoffice_ip = ?, backoffice_ua = ? WHERE id = ?`,
		at, ip, text.Truncate(userAgent, MaxUserAgentChars), sessionID); err != nil {
		return fmt.Errorf("auth: record backoffice entry: %w", err)
	}
	return nil
}

// SetBackofficeAt records, or with zero clears, this session's entry to the
// backoffice.
func (s *SessionStore) SetBackofficeAt(ctx context.Context, sessionID string, at int64) error {
	if _, err := s.db.Exec(ctx, `UPDATE sessions SET backoffice_at = ? WHERE id = ?`, at, sessionID); err != nil {
		return fmt.Errorf("auth: record backoffice entry: %w", err)
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

// ListByUser is this account's own "signed-in devices" list: its full
// sessions, most recently active first. Pending ones are excluded — a
// password proved with no code yet is not a signed-in device, and showing
// one in this list would offer a "sign out" button for something that was
// never signed in to begin with.
func (s *SessionStore) ListByUser(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, user_id, created_at, expires_at, last_seen_at, ip, user_agent
		 FROM sessions WHERE user_id = ? AND two_factor_pending = ?
		 ORDER BY last_seen_at DESC`, userID, false)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var record Session
		if err := rows.Scan(&record.ID, &record.UserID, &record.CreatedAt, &record.ExpiresAt,
			&record.LastSeenAt, &record.IP, &record.UserAgent); err != nil {
			return nil, fmt.Errorf("auth: scan session: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// DeleteByPrefixForUser removes one of this account's own sessions by the
// opaque id the profile screen shows it as — the first 16 hex characters of
// the stored session id, never the token — scoped to the account so one
// caller can never reach another's row by guessing at it. It reports whether
// a row actually matched, which is the 404-or-not decision the caller makes.
//
// ValidSessionRef must have refused anything that is not exactly that shape
// before this is called: prefix becomes the left side of a LIKE, and an
// unchecked wildcard here could only ever widen the match within this same
// account's own rows, but "delete one session" silently deleting several
// because of a stray "%" is still the wrong answer.
func (s *SessionStore) DeleteByPrefixForUser(ctx context.Context, userID, prefix string) (bool, error) {
	result, err := s.db.Exec(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND id LIKE ? AND two_factor_pending = ?`,
		userID, prefix+"%", false)
	if err != nil {
		return false, fmt.Errorf("auth: revoke session: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, nil
	}
	return removed > 0, nil
}

// DeleteOthers is "sign out other devices": every session on the account
// except keepSessionID. Unlike DeleteByUser ("sign out everywhere"), the
// caller's own session survives it.
func (s *SessionStore) DeleteOthers(ctx context.Context, userID, keepSessionID string) (int64, error) {
	result, err := s.db.Exec(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND id <> ? AND two_factor_pending = ?`,
		userID, keepSessionID, false)
	if err != nil {
		return 0, fmt.Errorf("auth: revoke other sessions: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return removed, nil
}

// SetDeviceID attaches a device identity to a session, once RecordDevice has
// resolved one for it. Takes a Queryer, like DeleteByUser, so RecordDevice
// can write it inside the same transaction as the device row it belongs
// beside — both commit together, or neither does.
func (s *SessionStore) SetDeviceID(ctx context.Context, q database.Queryer, sessionID, deviceID string) error {
	if q == nil {
		q = s.db
	}
	if _, err := q.Exec(ctx, `UPDATE sessions SET device_id = ? WHERE id = ?`, deviceID, sessionID); err != nil {
		return fmt.Errorf("auth: attach device: %w", err)
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
