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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

type Role string

const (
	RoleUser       Role = "user"
	RoleAdmin      Role = "admin"
	RoleSuperAdmin Role = "super_admin"
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
	ID               string   `json:"id"`
	Username         string   `json:"username"`
	Email            string   `json:"email"`
	Nickname         string   `json:"nickname"`
	Avatar           string   `json:"avatar"`
	Bio              string   `json:"bio"`
	Role             Role     `json:"role"`
	AdminPermissions []string `json:"admin_permissions"`
	GroupID          string   `json:"group_id"`
	GroupExpiresAt   int64    `json:"group_expires_at"`
	Status           Status   `json:"status"`
	// An administrator-supplied explanation shown to the account owner when
	// sign-in is refused. Empty when no reason was provided.
	BanReason string `json:"ban_reason"`
	// Whether the address above has been confirmed. True for every
	// account that predates verification, and for one with no address:
	// there is nothing to confirm and nothing to hold back.
	EmailVerified bool `json:"email_verified"`
	// When the address was shown to belong to this account, by a mailed
	// link or code or by a provider that checks addresses; 0 if it never
	// was. Not EmailVerified, which is also true wherever confirmation is
	// switched off: this is what may be relied on to say that somebody
	// signing in elsewhere with the same address is the same person.
	EmailProvenAt int64 `json:"-"`
	CreatedAt     int64 `json:"created_at"`
	UpdatedAt     int64 `json:"updated_at"`
	LastLoginAt   int64 `json:"last_login_at"`
	LastActiveAt  int64 `json:"last_active_at"`
	// Where this account was created from. Read by the backoffice, which is
	// where the per-address registration limit is configured and therefore
	// where "why was this address refused" gets asked.
	SignupIP string `json:"signup_ip"`
	// What the registering client identified itself as. Kept with the account
	// because a later session cannot recover the client that created it.
	SignupUserAgent string `json:"signup_user_agent"`
	// A user-level brake over the group's API grant. Zero means a manual or
	// policy restriction has no automatic expiry; the boolean distinguishes
	// that from an unrestricted account.
	APIRestricted        bool   `json:"api_restricted"`
	APIRestrictedUntil   int64  `json:"api_restricted_until"`
	APIRestrictionSource string `json:"api_restriction_source"`
	// When the second sign-in step was switched on; zero while it is off.
	// The secret itself lives with internal/auth and never rides on this.
	TwoFactorAt int64 `json:"two_factor_at"`
	// The values of the columns plugins added — see DefineField. Nil on a
	// build that has none.
	Fields map[string]string `json:"fields,omitempty"`
}

func (u User) IsAdmin() bool      { return u.Role == RoleAdmin || u.IsSuperAdmin() }
func (u User) IsSuperAdmin() bool { return u.Role == RoleSuperAdmin }
func (u User) IsActive() bool     { return u.Status == StatusActive }

// TwoFactorEnabled reports whether signing in asks for a code as well.
func (u User) TwoFactorEnabled() bool { return u.TwoFactorAt > 0 }

func (u User) APIRestrictedAt(now time.Time) bool {
	return u.APIRestricted && (u.APIRestrictedUntil == 0 || u.APIRestrictedUntil > now.UnixMilli())
}

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
	MaxAvatarChars    = 8 * 1024
	MaxEmailChars     = 254
	MaxBanReasonChars = 500
	// The value comes from an untrusted request header and the user row is read
	// on every authenticated request, so it gets the same bound as session UAs.
	MaxSignupUserAgentChars = 200
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

type Store struct {
	db   *database.DB
	gate plugingate.Gate
	set  atomic.Pointer[fieldSet]

	// The fields plugins installed while the server runs added; see
	// AddPluginFields.
	dynMu   sync.Mutex
	dynamic []Field
}

func NewStore(db *database.DB) *Store { return &Store{db: db} }

// The core's columns. Every query reads them through columnList, which adds
// the fields plugins defined.
const baseColumns = `id, username, email, nickname, avatar, bio, role, group_id, status,
	email_verified, created_at, updated_at, last_login_at, signup_ip, signup_user_agent,
	api_restricted, api_restricted_until, api_restriction_source, group_expires_at, admin_permissions, last_active_at,
	two_factor_at, ban_reason, email_proven_at`

type CreateInput struct {
	Username string
	Email    string
	// Plugin field values, by key — see DefineField.
	Fields       map[string]string
	PasswordHash string
	Nickname     string
	Role         Role
	// Set false only when this account must confirm its address before
	// it can spend anything.
	Unverified bool
	// The address arrived already proved, by a provider that checks them.
	EmailProven bool
	GroupID     string
	Status      Status
	BanReason   string
	// The address this account was created from, for the per-address
	// registration limit. Empty where it could not be resolved.
	SignupIP        string
	SignupUserAgent string

	APIRestricted        bool
	APIRestrictedUntil   int64
	APIRestrictionSource string
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
	extra, err := s.CheckFields(in.Fields)
	if err != nil {
		return User{}, err
	}
	nickname, err := checkNickname(in.Nickname)
	if err != nil {
		return User{}, err
	}

	now := time.Now().UnixMilli()
	record := User{
		ID:        id.New(),
		Username:  username,
		Email:     email,
		Nickname:  nickname,
		Role:      orDefault(in.Role, RoleUser),
		GroupID:   in.GroupID,
		Status:    orDefault(in.Status, StatusActive),
		BanReason: strings.TrimSpace(in.BanReason),
		// An account with no address has nothing to confirm, so it is
		// never held back for not having confirmed it.
		EmailVerified: !in.Unverified || email == "",
		CreatedAt:     now,
		UpdatedAt:     now,
		// Carried on the record as well as written to the row: this is what
		// the caller hands back to the browser, and a field that is correct
		// only after the account is read a second time is a field that reads
		// as missing on the response that creates it.
		SignupIP:             in.SignupIP,
		SignupUserAgent:      text.Truncate(in.SignupUserAgent, MaxSignupUserAgentChars),
		APIRestricted:        in.APIRestricted,
		APIRestrictedUntil:   in.APIRestrictedUntil,
		APIRestrictionSource: in.APIRestrictionSource,
	}
	if in.EmailProven && email != "" {
		record.EmailProvenAt = now
	}

	// Every defined field is written, empty where no value was given: the
	// record handed back carries all of them, and a column left to its
	// default would have to be assumed to be ''.
	fields := s.active().list
	fieldColumns, fieldMarks := "", ""
	fieldArgs := make([]any, 0, len(fields))
	if len(fields) > 0 {
		record.Fields = make(map[string]string, len(fields))
		for _, f := range fields {
			fieldColumns += ", " + f.Key
			fieldMarks += ", ?"
			fieldArgs = append(fieldArgs, extra[f.Key])
			record.Fields[f.Key] = extra[f.Key]
		}
	}
	args := []any{
		record.ID, record.Username, strings.ToLower(record.Username),
		record.Email, strings.ToLower(record.Email), in.PasswordHash, record.Nickname,
		record.Role, nullable(record.GroupID), record.Status, record.EmailVerified,
		record.CreatedAt, record.UpdatedAt, in.SignupIP, record.SignupUserAgent, record.APIRestricted,
		record.APIRestrictedUntil, record.APIRestrictionSource, record.BanReason, record.EmailProvenAt,
	}
	_, err = q.Exec(ctx, `INSERT INTO users
		(id, username, username_lower, email, email_lower, password_hash, nickname, avatar, bio,
		 role, group_id, status, email_verified, created_at, updated_at, last_login_at, signup_ip,
		 signup_user_agent, api_restricted, api_restricted_until, api_restriction_source, ban_reason,
		 email_proven_at`+fieldColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?`+fieldMarks+`)`,
		append(args, fieldArgs...)...)
	if err != nil {
		// Both engines report a violated unique index without naming a
		// portable error code, so the message is matched instead. The check
		// is only ever reached after an explicit availability query, so this
		// path is the concurrent-registration race, not the common case.
		return User{}, s.translateUniqueViolation(err, email != "")
	}
	return record, nil
}

// MarkEmailProven records that a provider which checks addresses has vouched
// for the account's current one. Matched on the address, so a proof for an
// address the account has since left lands nowhere.
func (s *Store) MarkEmailProven(ctx context.Context, q database.Queryer, userID, email string) error {
	if q == nil {
		q = s.db
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	_, err := q.Exec(ctx,
		`UPDATE users SET email_proven_at = ? WHERE id = ? AND email_lower = ? AND email_proven_at = 0`,
		time.Now().UnixMilli(), userID, email)
	if err != nil {
		return fmt.Errorf("user: mark email proven: %w", err)
	}
	return nil
}

func (s *Store) ByID(ctx context.Context, q database.Queryer, userID string) (User, error) {
	if q == nil {
		q = s.db
	}
	record, err := s.scan(q.QueryRow(ctx, `SELECT `+s.columns()+` FROM users WHERE id = ?`, userID))
	if err != nil {
		return User{}, err
	}
	return s.ResolveMembership(ctx, q, record)
}

// ByEmail resolves an address to the account holding it.
//
// Folded the way the column is written rather than compared with EqualFold,
// which applies Unicode simple case folding and would read two different
// stored addresses as one. This is the lookup a provider sign-in makes before
// it decides whether to open a second account, so "the same address" has to
// mean exactly what the uniqueness index means by it.
//
// Takes a Queryer because that decision is a check followed by a write and
// has to happen inside the transaction holding the lock.
func (s *Store) ByEmail(ctx context.Context, q database.Queryer, email string) (User, error) {
	if q == nil {
		q = s.db
	}
	folded := strings.ToLower(strings.TrimSpace(email))
	if folded == "" {
		return User{}, ErrNotFound
	}
	record, err := s.scan(q.QueryRow(ctx,
		`SELECT `+s.columns()+` FROM users WHERE email_lower <> '' AND email_lower = ?`, folded))
	if err != nil {
		return User{}, err
	}
	return s.ResolveMembership(ctx, q, record)
}

// ByField resolves the one account whose unique field key holds value.
// Callers arrive holding a value something has proved — an identity provider
// whose subject is that value, say — so a match is a person, not a claim to
// double-check.
func (s *Store) ByField(ctx context.Context, q database.Queryer, key, value string) (User, error) {
	if q == nil {
		q = s.db
	}
	value = strings.TrimSpace(value)
	unique := false
	for _, f := range s.active().list {
		if f.Key == key && f.Unique {
			unique = true
		}
	}
	if value == "" || !unique {
		return User{}, ErrNotFound
	}
	record, err := s.scan(q.QueryRow(ctx,
		`SELECT `+s.columns()+` FROM users WHERE `+key+` <> '' AND `+key+` = ?`, value))
	if err != nil {
		return User{}, err
	}
	return s.ResolveMembership(ctx, q, record)
}

// that has never had one — every account created by a provider sign-in.
//
// Separate from CredentialsByLogin because the callers are different
// questions: that one is "is this the right password", this one is "is a
// password one of the ways into this account", which is what the last way in
// may not be removed.
func (s *Store) PasswordHash(ctx context.Context, q database.Queryer, userID string) (string, error) {
	if q == nil {
		q = s.db
	}
	var hash string
	if err := q.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		if database.IsNotFound(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("user: read credential: %w", err)
	}
	return hash, nil
}

// CredentialsByLogin resolves a username or an email address to the account
// and its password hash. The only caller is the login flow.
func (s *Store) CredentialsByLogin(ctx context.Context, identifier string) (User, string, error) {
	folded := strings.ToLower(strings.TrimSpace(identifier))
	row := s.db.QueryRow(ctx,
		`SELECT `+s.columns()+`, password_hash FROM users
		 WHERE username_lower = ? OR (email_lower <> '' AND email_lower = ?)`,
		folded, folded)

	var hash string
	record, err := s.scan(row, &hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return User{}, "", ErrNotFound
		}
		return User{}, "", fmt.Errorf("user: load credentials: %w", err)
	}
	record, err = s.ResolveMembership(ctx, nil, record)
	return record, hash, err
}

// Exists answers the availability check the registration form makes before it
// attempts an insert, so the common "that name is taken" case is a friendly
// message rather than a constraint error. takenField names the first unique
// plugin field in values that another account already holds.
func (s *Store) Exists(ctx context.Context, q database.Queryer, username, email string, values map[string]string) (usernameTaken, emailTaken bool, takenField string, err error) {
	if q == nil {
		q = s.db
	}
	wantUser := strings.ToLower(strings.TrimSpace(username))
	wantMail := strings.ToLower(strings.TrimSpace(email))

	var (
		checked []string
		want    []string
	)
	selects := "username_lower, email_lower"
	where := "username_lower = ? OR (email_lower <> '' AND email_lower = ?)"
	args := []any{wantUser, wantMail}
	for _, f := range s.active().list {
		value := strings.TrimSpace(values[f.Key])
		if !f.Unique || value == "" {
			continue
		}
		checked = append(checked, f.Key)
		want = append(want, value)
		selects += ", " + f.Key
		where += " OR (" + f.Key + " <> '' AND " + f.Key + " = ?)"
		args = append(args, value)
	}

	rows, err := q.Query(ctx, `SELECT `+selects+` FROM users WHERE `+where, args...)
	if err != nil {
		return false, false, "", fmt.Errorf("user: availability check: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var haveUser, haveMail string
		have := make([]string, len(checked))
		dest := []any{&haveUser, &haveMail}
		for i := range have {
			dest = append(dest, &have[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return false, false, "", fmt.Errorf("user: availability scan: %w", err)
		}
		if haveUser == wantUser {
			usernameTaken = true
		}
		if wantMail != "" && haveMail == wantMail {
			emailTaken = true
		}
		for i, key := range checked {
			if takenField == "" && have[i] == want[i] {
				takenField = key
			}
		}
	}
	return usernameTaken, emailTaken, takenField, rows.Err()
}

// ProfileUpdate carries only the fields a user may change about themselves. A
// nil pointer means "leave alone", which is what lets one endpoint serve a
// form that submits a single field.
type ProfileUpdate struct {
	Nickname *string
	Avatar   *string
	Bio      *string
	Email    *string
	// Plugin field values to write, by key; a key absent is left alone.
	Fields map[string]string
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
		// Proof belongs to an address, so moving to another one gives it up.
		// Both engines read the old row on the right of SET, so email_lower
		// here is the address being left.
		sets = append(sets, "email_proven_at = CASE WHEN email_lower = ? THEN email_proven_at ELSE 0 END",
			"email = ?", "email_lower = ?")
		args = append(args, strings.ToLower(value), value, strings.ToLower(value))
	}
	if len(in.Fields) > 0 {
		values, err := s.CheckFields(in.Fields)
		if err != nil {
			return User{}, err
		}
		more, moreArgs := s.fieldSets(values)
		sets = append(sets, more...)
		args = append(args, moreArgs...)
	}

	if len(sets) == 0 {
		return s.ByID(ctx, q, userID)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UnixMilli(), userID)

	if _, err := q.Exec(ctx,
		`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return User{}, s.translateUniqueViolation(err, in.Email != nil)
	}
	return s.ByID(ctx, q, userID)
}

// AdminUpdate is everything only an administrator may change.
type AdminUpdate struct {
	Role             *Role
	AdminPermissions *[]string
	GroupID          *string
	GroupExpiresAt   *int64
	Status           *Status
	BanReason        *string
	// Plugin field values to write, by key; a key absent is left alone.
	Fields map[string]string
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
	if in.AdminPermissions != nil {
		encoded, err := json.Marshal(*in.AdminPermissions)
		if err != nil {
			return User{}, err
		}
		sets = append(sets, "admin_permissions = ?")
		args = append(args, string(encoded))
	}
	if in.GroupID != nil {
		// An ordinary save of the same group keeps its term. Moving somebody
		// elsewhere must not carry an old expiry into the new membership.
		if in.GroupExpiresAt == nil {
			sets = append(sets, "group_expires_at = CASE WHEN group_id = ? THEN group_expires_at ELSE 0 END")
			args = append(args, nullable(*in.GroupID))
		}
		sets = append(sets, "group_id = ?")
		args = append(args, nullable(*in.GroupID))
	}
	if in.GroupExpiresAt != nil {
		sets = append(sets, "group_expires_at = ?")
		args = append(args, *in.GroupExpiresAt)
	}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, orDefault(*in.Status, StatusActive))
	}
	if in.BanReason != nil {
		sets = append(sets, "ban_reason = ?")
		args = append(args, strings.TrimSpace(*in.BanReason))
	}
	if len(in.Fields) > 0 {
		values, err := s.CheckFields(in.Fields)
		if err != nil {
			return User{}, err
		}
		more, moreArgs := s.fieldSets(values)
		sets = append(sets, more...)
		args = append(args, moreArgs...)
	}
	if len(sets) == 0 {
		return s.ByID(ctx, q, userID)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UnixMilli(), userID)

	if _, err := q.Exec(ctx,
		`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		if key := s.takenField(strings.ToLower(err.Error())); key != "" {
			return User{}, &FieldError{Key: key, Reason: ErrFieldTaken}
		}
		return User{}, fmt.Errorf("user: admin update: %w", err)
	}
	return s.ByID(ctx, q, userID)
}

// UpdateAPIRestriction is separate from group membership: a review must be
// able to withhold programmatic access without also changing which models,
// quota, and interface capabilities the account inherits.
func (s *Store) UpdateAPIRestriction(
	ctx context.Context, q database.Queryer, userID string, restricted bool, until int64, source string,
) (User, error) {
	if q == nil {
		q = s.db
	}
	if !restricted {
		until = 0
		source = ""
	}
	_, err := q.Exec(ctx,
		`UPDATE users SET api_restricted = ?, api_restricted_until = ?,
		 api_restriction_source = ?, updated_at = ? WHERE id = ?`,
		restricted, until, strings.TrimSpace(source), time.Now().UnixMilli(), userID)
	if err != nil {
		return User{}, fmt.Errorf("user: update API restriction: %w", err)
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
	if err := s.ExpireMemberships(ctx, nil, time.Now()); err != nil {
		return nil, 0, err
	}
	where, args := filter.clauses(s.active().list)

	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("user: count: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+s.columns()+` FROM users`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, max(0, filter.Offset))...)
	if err != nil {
		return nil, 0, fmt.Errorf("user: list: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		record, err := s.scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, record)
	}
	return out, total, rows.Err()
}

// escapeLike neutralises the wildcards inside a value an operator typed, so
// a search for "_" matches an underscore rather than every account.
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}

func (filter ListFilter) clauses(fields []Field) (string, []any) {
	conditions := []string{}
	args := []any{}

	if search := strings.TrimSpace(filter.Search); search != "" {
		// LIKE with a lowered needle rather than ILIKE: the latter is
		// Postgres-only, and the folded columns already exist for login.
		//
		// The escape character is declared rather than assumed: SQLite has
		// none by default, so without it an operator searching for "_" gets
		// every account back and reads it as a match.
		pattern := "%" + escapeLike(strings.ToLower(search)) + "%"
		match := `username_lower LIKE ? ESCAPE '\' OR email_lower LIKE ? ESCAPE '\'` +
			` OR LOWER(nickname) LIKE ? ESCAPE '\'`
		args = append(args, pattern, pattern, pattern)
		for _, f := range fields {
			if f.Searchable {
				match += ` OR LOWER(` + f.Key + `) LIKE ? ESCAPE '\'`
				args = append(args, pattern)
			}
		}
		conditions = append(conditions, "("+match+")")
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

// Any answers the only question most callers of Count actually ask: is this
// instance still empty.
//
// Separate because it is on the path every client takes before the sign-in
// screen paints, and counting a whole table to learn whether it has a first
// row is work that grows with the instance to answer a question that does
// not. LIMIT 1 over ErrNoRows rather than SELECT EXISTS, which comes back an
// integer on SQLite and a boolean on Postgres.
func (s *Store) Any(ctx context.Context, q database.Queryer) (bool, error) {
	if q == nil {
		q = s.db
	}
	var found string
	err := q.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&found)
	if database.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("user: any: %w", err)
	}
	return true, nil
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
		RoleSuperAdmin, StatusActive, excluding).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("user: count admins: %w", err)
	}
	return count, nil
}

func (s *Store) Delete(ctx context.Context, q database.Queryer, userID string) error {
	if q == nil {
		q = s.db
	}
	// Personal invite codes carry owner_id rather than a foreign key with
	// ON DELETE CASCADE, because admin-issued codes use '' rather than NULL.
	if _, err := q.Exec(ctx, `DELETE FROM invite_codes WHERE owner_id = ?`, userID); err != nil {
		return fmt.Errorf("user: delete invite codes: %w", err)
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
	_, err := q.Exec(ctx, `UPDATE users SET group_id = ?, group_expires_at = 0, updated_at = ? WHERE group_id = ?`,
		nullable(to), time.Now().UnixMilli(), from)
	if err != nil {
		return fmt.Errorf("user: move group members: %w", err)
	}
	return nil
}

// --- scanning ---------------------------------------------------------------

// JoinColumns is the same column list qualified with a table alias, and
// ScanRow reads what either list selects.
//
// Both exist so a query that joins users to another table can be answered in
// one round trip without this package's column list being copied into the
// caller — a copy that goes wrong silently, because a column list and a scan
// list that disagree still compile.
//
// The session lookup calls this on every authenticated request with the same
// alias, so each alias's list is built once and kept.
func (s *Store) JoinColumns(alias string) string {
	set := s.active()
	if cached, ok := set.joined.Load(alias); ok {
		return cached.(string)
	}
	parts := strings.Split(set.columns, ",")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	joined := strings.Join(parts, ", ")
	set.joined.Store(alias, joined)
	return joined
}

// ScanRow reads what JoinColumns selected. A plugin switched between the two
// calls would misalign them, so a caller builds its query and scans within
// one request — the window is a query long, and the scan fails loudly
// rather than reading one column as another.
func (s *Store) ScanRow(row interface{ Scan(dest ...any) error }) (User, error) { return s.scan(row) }

type rowScanner interface{ Scan(dest ...any) error }

// scan reads what columns selects, then fills extra from whatever the query
// selected after it.
func (s *Store) scan(row rowScanner, extra ...any) (User, error) {
	fields := s.active().list
	var (
		record      User
		group       sql.NullString
		permissions string
	)
	values := make([]string, len(fields))
	dest := []any{&record.ID, &record.Username, &record.Email, &record.Nickname, &record.Avatar,
		&record.Bio, &record.Role, &group, &record.Status, &record.EmailVerified,
		&record.CreatedAt, &record.UpdatedAt, &record.LastLoginAt, &record.SignupIP,
		&record.SignupUserAgent, &record.APIRestricted, &record.APIRestrictedUntil,
		&record.APIRestrictionSource, &record.GroupExpiresAt, &permissions, &record.LastActiveAt,
		&record.TwoFactorAt, &record.BanReason, &record.EmailProvenAt}
	for i := range values {
		dest = append(dest, &values[i])
	}
	err := row.Scan(append(dest, extra...)...)
	if err != nil {
		if database.IsNotFound(err) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: scan: %w", err)
	}
	record.GroupID = group.String
	if err := json.Unmarshal([]byte(permissions), &record.AdminPermissions); err != nil {
		return User{}, fmt.Errorf("user: read permissions: %w", err)
	}
	if len(fields) > 0 {
		record.Fields = make(map[string]string, len(fields))
		for i, f := range fields {
			record.Fields[f.Key] = values[i]
		}
	}
	return record, nil
}

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

func (s *Store) translateUniqueViolation(err error, hadEmail bool) error {
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "unique") && !strings.Contains(message, "duplicate") {
		return fmt.Errorf("user: write: %w", err)
	}
	if key := s.takenField(message); key != "" {
		return &FieldError{Key: key, Reason: ErrFieldTaken}
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

// CountFromIP is how many accounts one address has created since a moment.
//
// The registration limit is checked against this and enforced by it, and both
// halves are the database rather than a counter in memory: a process restart
// must not hand an attacker a fresh allowance, and two instances against one
// database have to agree on the number.
//
// An empty address is never counted. Where the client address could not be
// resolved the honest answer is that there is nothing to attribute, and
// attributing it to "" would put every such account in one bucket and lock
// the instance out on the operator's first misconfigured proxy.
func (s *Store) CountFromIP(ctx context.Context, q database.Queryer, ip string, since int64) (int, error) {
	if q == nil {
		q = s.db
	}
	if strings.TrimSpace(ip) == "" {
		return 0, nil
	}

	var count int
	err := q.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE signup_ip = ? AND created_at >= ?`, ip, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("user: count from ip: %w", err)
	}
	return count, nil
}
