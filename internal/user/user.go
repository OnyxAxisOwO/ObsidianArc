// Package user owns the accounts table: who exists, what they are called,
// what role they hold, and which group they belong to.
//
// It deliberately knows nothing about passwords beyond storing an opaque
// hash — hashing, verifying and session handling all live in internal/auth,
// so there is exactly one place that can get the credential handling wrong.
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

// User is the shape every layer above the store passes around, and the shape
// serialised to the browser. The password hash is deliberately not a field:
// it can only be obtained through CredentialsByLogin, which is called from
// exactly one place.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	Bio      string `json:"bio"`
	Role     Role   `json:"role"`
	GroupID  string `json:"group_id"`
	Status   Status `json:"status"`
	// Whether the address above has been confirmed. True for every
	// account that predates verification, and for one with no address:
	// there is nothing to confirm and nothing to hold back.
	EmailVerified bool  `json:"email_verified"`
	CreatedAt     int64 `json:"created_at"`
	UpdatedAt     int64 `json:"updated_at"`
	LastLoginAt   int64 `json:"last_login_at"`
}

func (u User) IsAdmin() bool  { return u.Role == RoleAdmin }
func (u User) IsActive() bool { return u.Status == StatusActive }

// DisplayName is what the interface shows: the nickname when set, the
// username otherwise. One definition so the header, the admin list and the
// transcript cannot disagree.
func (u User) DisplayName() string {
	if u.Nickname != "" {
		return u.Nickname
	}
	return u.Username
}

var (
	ErrNotFound          = errors.New("user: not found")
	ErrUsernameTaken     = errors.New("user: username already taken")
	ErrEmailTaken        = errors.New("user: email already registered")
	ErrInvalidUsername   = errors.New("user: username must be 3-32 characters of letters, digits, dot, dash or underscore")
	ErrInvalidEmail      = errors.New("user: email address is not valid")
	ErrNicknameTooLong   = errors.New("user: nickname must be 32 characters or fewer")
	ErrBioTooLong        = errors.New("user: bio must be 500 characters or fewer")
	ErrAvatarTooLong     = errors.New("user: avatar is too large")
	ErrLastAdminDemotion = errors.New("user: the last administrator cannot be demoted or disabled")
)

const (
	MaxNicknameChars = 32
	MaxBioChars      = 500
	// An avatar is a URL or a small inline image. The cap is what keeps the
	// users table — read on every authenticated request — from growing a
	// megabyte-per-row column.
	MaxAvatarChars = 8 * 1024
	MaxEmailChars  = 254
)

var (
	usernameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)
	// Deliberately loose. Anything stricter rejects addresses that are
	// perfectly valid, and this server never sends mail, so the address is
	// an identifier rather than a delivery route.
	emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s.]+\.[^@\s]+$`)
)

func ValidateUsername(value string) error {
	if !usernameRE.MatchString(value) {
		return ErrInvalidUsername
	}
	return nil
}

func ValidateEmail(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > MaxEmailChars || !emailRE.MatchString(value) {
		return ErrInvalidEmail
	}
	return nil
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const columns = `id, username, email, nickname, avatar, bio, role, group_id, status,
	email_verified, created_at, updated_at, last_login_at`

type CreateInput struct {
	Username     string
	Email        string
	PasswordHash string
	Nickname     string
	Role         Role
	// Set false only when this account must confirm its address before
	// it can spend anything.
	Unverified bool
	GroupID    string
	Status     Status
}

func (s *Store) Create(ctx context.Context, q database.Queryer, in CreateInput) (User, error) {
	if q == nil {
		q = s.db
	}
	username := strings.TrimSpace(in.Username)
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	email := strings.TrimSpace(in.Email)
	if err := ValidateEmail(email); err != nil {
		return User{}, err
	}
	nickname, err := checkNickname(in.Nickname)
	if err != nil {
		return User{}, err
	}

	now := time.Now().UnixMilli()
	record := User{
		ID:       id.New(),
		Username: username,
		Email:    email,
		Nickname: nickname,
		Role:     orDefault(in.Role, RoleUser),
		GroupID:  in.GroupID,
		Status:   orDefault(in.Status, StatusActive),
		// An account with no address has nothing to confirm, so it is
		// never held back for not having confirmed it.
		EmailVerified: !in.Unverified || email == "",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	_, err = q.Exec(ctx, `INSERT INTO users
		(id, username, username_lower, email, email_lower, password_hash, nickname, avatar, bio,
		 role, group_id, status, email_verified, created_at, updated_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, ?, ?, 0)`,
		record.ID, record.Username, strings.ToLower(record.Username),
		record.Email, strings.ToLower(record.Email), in.PasswordHash, record.Nickname,
		record.Role, nullable(record.GroupID), record.Status, record.EmailVerified,
		record.CreatedAt, record.UpdatedAt)
	if err != nil {
		// Both engines report a violated unique index without naming a
		// portable error code, so the message is matched instead. The check
		// is only ever reached after an explicit availability query, so this
		// path is the concurrent-registration race, not the common case.
		return User{}, translateUniqueViolation(err, email != "")
	}
	return record, nil
}

func (s *Store) ByID(ctx context.Context, q database.Queryer, userID string) (User, error) {
	if q == nil {
		q = s.db
	}
	return scanUser(q.QueryRow(ctx, `SELECT `+columns+` FROM users WHERE id = ?`, userID))
}

// CredentialsByLogin resolves a username or an email address to the account
// and its password hash. The only caller is the login flow.
func (s *Store) CredentialsByLogin(ctx context.Context, identifier string) (User, string, error) {
	folded := strings.ToLower(strings.TrimSpace(identifier))
	row := s.db.QueryRow(ctx,
		`SELECT `+columns+`, password_hash FROM users
		 WHERE username_lower = ? OR (email_lower <> '' AND email_lower = ?)`,
		folded, folded)

	var (
		record User
		hash   string
		group  sql.NullString
	)
	err := row.Scan(&record.ID, &record.Username, &record.Email, &record.Nickname, &record.Avatar,
		&record.Bio, &record.Role, &group, &record.Status, &record.EmailVerified,
		&record.CreatedAt, &record.UpdatedAt, &record.LastLoginAt, &hash)
	if err != nil {
		if database.IsNotFound(err) {
			return User{}, "", ErrNotFound
		}
		return User{}, "", fmt.Errorf("user: load credentials: %w", err)
	}
	record.GroupID = group.String
	return record, hash, nil
}

// Exists answers the availability check the registration form makes before it
// attempts an insert, so the common "that name is taken" case is a friendly
// message rather than a constraint error.
func (s *Store) Exists(ctx context.Context, q database.Queryer, username, email string) (usernameTaken, emailTaken bool, err error) {
	if q == nil {
		q = s.db
	}
	rows, err := q.Query(ctx,
		`SELECT username_lower, email_lower FROM users
		 WHERE username_lower = ? OR (email_lower <> '' AND email_lower = ?)`,
		strings.ToLower(strings.TrimSpace(username)), strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return false, false, fmt.Errorf("user: availability check: %w", err)
	}
	defer rows.Close()

	wantUser := strings.ToLower(strings.TrimSpace(username))
	wantMail := strings.ToLower(strings.TrimSpace(email))
	for rows.Next() {
		var haveUser, haveMail string
		if err := rows.Scan(&haveUser, &haveMail); err != nil {
			return false, false, fmt.Errorf("user: availability scan: %w", err)
		}
		if haveUser == wantUser {
			usernameTaken = true
		}
		if wantMail != "" && haveMail == wantMail {
			emailTaken = true
		}
	}
	return usernameTaken, emailTaken, rows.Err()
}

// ProfileUpdate carries only the fields a user may change about themselves. A
// nil pointer means "leave alone", which is what lets one endpoint serve a
// form that submits a single field.
type ProfileUpdate struct {
	Nickname *string
	Avatar   *string
	Bio      *string
	Email    *string
}

func (s *Store) UpdateProfile(ctx context.Context, q database.Queryer, userID string, in ProfileUpdate) (User, error) {
	if q == nil {
		q = s.db
	}
	sets := []string{}
	args := []any{}

	if in.Nickname != nil {
		value, err := checkNickname(*in.Nickname)
		if err != nil {
			return User{}, err
		}
		sets = append(sets, "nickname = ?")
		args = append(args, value)
	}
	if in.Avatar != nil {
		value := strings.TrimSpace(*in.Avatar)
		if len(value) > MaxAvatarChars {
			return User{}, ErrAvatarTooLong
		}
		sets = append(sets, "avatar = ?")
		args = append(args, value)
	}
	if in.Bio != nil {
		value := strings.TrimSpace(*in.Bio)
		if utf8.RuneCountInString(value) > MaxBioChars {
			return User{}, ErrBioTooLong
		}
		sets = append(sets, "bio = ?")
		args = append(args, value)
	}
	if in.Email != nil {
		value := strings.TrimSpace(*in.Email)
		if err := ValidateEmail(value); err != nil {
			return User{}, err
		}
		sets = append(sets, "email = ?", "email_lower = ?")
		args = append(args, value, strings.ToLower(value))
	}

	if len(sets) == 0 {
		return s.ByID(ctx, q, userID)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UnixMilli(), userID)

	if _, err := q.Exec(ctx,
		`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return User{}, translateUniqueViolation(err, in.Email != nil)
	}
	return s.ByID(ctx, q, userID)
}

// AdminUpdate is everything only an administrator may change.
type AdminUpdate struct {
	Role    *Role
	GroupID *string
	Status  *Status
}

func (s *Store) UpdateAdminFields(ctx context.Context, q database.Queryer, userID string, in AdminUpdate) (User, error) {
	if q == nil {
		q = s.db
	}
	sets := []string{}
	args := []any{}

	if in.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, orDefault(*in.Role, RoleUser))
	}
	if in.GroupID != nil {
		sets = append(sets, "group_id = ?")
		args = append(args, nullable(*in.GroupID))
	}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, orDefault(*in.Status, StatusActive))
	}
	if len(sets) == 0 {
		return s.ByID(ctx, q, userID)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UnixMilli(), userID)

	if _, err := q.Exec(ctx,
		`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return User{}, fmt.Errorf("user: admin update: %w", err)
	}
	return s.ByID(ctx, q, userID)
}

func (s *Store) SetPasswordHash(ctx context.Context, q database.Queryer, userID, hash string) error {
	if q == nil {
		q = s.db
	}
	_, err := q.Exec(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		hash, time.Now().UnixMilli(), userID)
	if err != nil {
		return fmt.Errorf("user: set password: %w", err)
	}
	return nil
}

func (s *Store) MarkLogin(ctx context.Context, userID string, at int64) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, at, userID)
	if err != nil {
		return fmt.Errorf("user: mark login: %w", err)
	}
	return nil
}

// ListFilter drives the admin user list. Search matches the username,
// nickname or email; the empty value of every field means "no filter".
type ListFilter struct {
	Search  string
	Role    Role
	Status  Status
	GroupID string
	Limit   int
	Offset  int
}

func (s *Store) List(ctx context.Context, filter ListFilter) ([]User, int, error) {
	where, args := filter.clauses()

	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("user: count: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+columns+` FROM users`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, max(0, filter.Offset))...)
	if err != nil {
		return nil, 0, fmt.Errorf("user: list: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		record, err := scanUserRows(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, record)
	}
	return out, total, rows.Err()
}

func (filter ListFilter) clauses() (string, []any) {
	conditions := []string{}
	args := []any{}

	if search := strings.TrimSpace(filter.Search); search != "" {
		// LIKE with a lowered needle rather than ILIKE: the latter is
		// Postgres-only, and the folded columns already exist for login.
		pattern := "%" + strings.ToLower(search) + "%"
		conditions = append(conditions,
			"(username_lower LIKE ? OR email_lower LIKE ? OR LOWER(nickname) LIKE ?)")
		args = append(args, pattern, pattern, pattern)
	}
	if filter.Role != "" {
		conditions = append(conditions, "role = ?")
		args = append(args, filter.Role)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.GroupID != "" {
		conditions = append(conditions, "group_id = ?")
		args = append(args, filter.GroupID)
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func (s *Store) Count(ctx context.Context, q database.Queryer) (int, error) {
	if q == nil {
		q = s.db
	}
	var count int
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("user: count: %w", err)
	}
	return count, nil
}

// CountActiveAdmins guards the "do not lock everyone out" rule: demoting,
// disabling or deleting the final administrator has to fail.
func (s *Store) CountActiveAdmins(ctx context.Context, q database.Queryer, excluding string) (int, error) {
	if q == nil {
		q = s.db
	}
	var count int
	err := q.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE role = ? AND status = ? AND id <> ?`,
		RoleAdmin, StatusActive, excluding).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("user: count admins: %w", err)
	}
	return count, nil
}

func (s *Store) Delete(ctx context.Context, q database.Queryer, userID string) error {
	if q == nil {
		q = s.db
	}
	if _, err := q.Exec(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("user: delete: %w", err)
	}
	return nil
}

// MoveGroupMembers reassigns everyone in one group, which is what a group
// deletion needs before the group row can go.
func (s *Store) MoveGroupMembers(ctx context.Context, q database.Queryer, from, to string) error {
	if q == nil {
		q = s.db
	}
	_, err := q.Exec(ctx, `UPDATE users SET group_id = ?, updated_at = ? WHERE group_id = ?`,
		nullable(to), time.Now().UnixMilli(), from)
	if err != nil {
		return fmt.Errorf("user: move group members: %w", err)
	}
	return nil
}

// --- scanning ---------------------------------------------------------------

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(row rowScanner) (User, error) {
	var (
		record User
		group  sql.NullString
	)
	err := row.Scan(&record.ID, &record.Username, &record.Email, &record.Nickname, &record.Avatar,
		&record.Bio, &record.Role, &group, &record.Status, &record.EmailVerified,
		&record.CreatedAt, &record.UpdatedAt, &record.LastLoginAt)
	if err != nil {
		if database.IsNotFound(err) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: scan: %w", err)
	}
	record.GroupID = group.String
	return record, nil
}

func scanUserRows(rows *sql.Rows) (User, error) { return scanUser(rows) }

// --- helpers ----------------------------------------------------------------

func checkNickname(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if utf8.RuneCountInString(trimmed) > MaxNicknameChars {
		return "", ErrNicknameTooLong
	}
	return trimmed, nil
}

// An empty group is NULL rather than ”, so the foreign key stays satisfiable
// and "no group" is one value instead of two.
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func orDefault[T ~string](value, fallback T) T {
	if value == "" {
		return fallback
	}
	return value
}

func translateUniqueViolation(err error, hadEmail bool) error {
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "unique") && !strings.Contains(message, "duplicate") {
		return fmt.Errorf("user: write: %w", err)
	}
	switch {
	case strings.Contains(message, "email"):
		return ErrEmailTaken
	case strings.Contains(message, "username"):
		return ErrUsernameTaken
	case hadEmail:
		return ErrEmailTaken
	default:
		return ErrUsernameTaken
	}
}
