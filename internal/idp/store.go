package idp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Everything this feature stores: the applications, what each account has
// agreed to, the codes in flight, and the tokens outstanding.
//
// Codes and tokens are held as digests, the way an API key is. The row is
// readable by anything that can read the database, and both of these are a
// way into an account — so what is stored is enough to recognise a value that
// is presented and not enough to produce one.

// Digest maps a presented secret to the column that finds its row.
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

const appColumns = `id, client_id, name, description, redirect_uris, scopes,
	trusted, disabled, created_by, created_at, updated_at, secret_hash`

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) queryer(q database.Queryer) database.Queryer {
	if q == nil {
		return s.db
	}
	return q
}

// --- applications -------------------------------------------------------------

type CreateAppInput struct {
	Name        string
	Description string
	// One per line, as the form collects them.
	RedirectURIs string
	Scopes       []string
	Trusted      bool
	// A public application gets no secret and must use PKCE.
	Public    bool
	CreatedBy string
}

// The shape of the two values an application is configured with. The client
// id is not a secret — it travels in every authorisation URL — but it is
// long and random anyway, so that a list of applications is not something an
// outsider can guess their way through.
const (
	clientIDBytes = 16
	secretBytes   = 32
)

// CreateApp registers an application and returns its secret, once.
func (s *Store) CreateApp(ctx context.Context, in CreateAppInput) (App, string, error) {
	name, err := checkName(in.Name)
	if err != nil {
		return App{}, "", err
	}
	redirects, err := ParseRedirectURIs(in.RedirectURIs)
	if err != nil {
		return App{}, "", err
	}
	scopes := normaliseScopes(in.Scopes)
	description := strings.TrimSpace(in.Description)
	if len([]rune(description)) > MaxDescriptionChars {
		description = string([]rune(description)[:MaxDescriptionChars])
	}

	now := time.Now().UnixMilli()
	record := App{
		ID:           id.New(),
		ClientID:     id.Secret(clientIDBytes),
		Name:         name,
		Description:  description,
		RedirectURIs: redirects,
		Scopes:       scopes,
		Trusted:      in.Trusted,
		Confidential: !in.Public,
		CreatedBy:    in.CreatedBy,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	secret := ""
	hash := ""
	if record.Confidential {
		secret = id.Secret(secretBytes)
		hash = Digest(secret)
	}

	// The ceiling is a check followed by an insert, so it holds the instance
	// lock the way every other instance-wide count does.
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_apps`).Scan(&count); err != nil {
			return fmt.Errorf("idp: count applications: %w", err)
		}
		if count >= MaxApps {
			return fmt.Errorf("idp: this instance already has the maximum of %d applications", MaxApps)
		}
		_, err := tx.Exec(ctx, `INSERT INTO oauth_apps
			(id, client_id, secret_hash, name, description, redirect_uris, scopes,
			 trusted, disabled, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.ClientID, hash, record.Name, record.Description,
			strings.Join(record.RedirectURIs, "\n"), strings.Join(record.Scopes, " "),
			record.Trusted, false, nullable(in.CreatedBy), record.CreatedAt, record.UpdatedAt)
		return err
	})
	if err != nil {
		return App{}, "", err
	}
	return record, secret, nil
}

type AppUpdate struct {
	Name         *string
	Description  *string
	RedirectURIs *string
	Scopes       *[]string
	Trusted      *bool
	Disabled     *bool
}

// ErrTrustedApplication is what anybody short of a super administrator is
// told when they would register, change or remove a trusted application, or
// make one trusted. Trust lets an application skip the consent screen for
// everybody who signs in, so the decision belongs to the operator and not to
// whoever was delegated the security page.
var ErrTrustedApplication = errors.New("idp: only a super administrator may change a trusted application")

// lockApp holds the application's row until the transaction ends. The
// statement changes nothing; it is there for the lock. A trust check is a read
// followed by a write, and without the lock a concurrent change to trust can
// land between the two.
func lockApp(ctx context.Context, tx *database.Tx, appID string) error {
	result, err := tx.Exec(ctx, `UPDATE oauth_apps SET updated_at = updated_at WHERE id = ?`, appID)
	if err != nil {
		return fmt.Errorf("idp: lock application: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("idp: lock application: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateApp applies in to one application. superAdmin is the caller's
// IsSuperAdmin answer: nobody else changes a trusted application, and nobody
// else makes one trusted.
func (s *Store) UpdateApp(ctx context.Context, appID string, in AppUpdate, superAdmin bool) (App, error) {
	var record App
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockApp(ctx, tx, appID); err != nil {
			return err
		}
		current, err := s.AppByID(ctx, tx, appID)
		if err != nil {
			return err
		}
		if !superAdmin && (current.Trusted || (in.Trusted != nil && *in.Trusted)) {
			return ErrTrustedApplication
		}
		record = current
		if in.Name != nil {
			name, err := checkName(*in.Name)
			if err != nil {
				return err
			}
			record.Name = name
		}
		if in.Description != nil {
			description := strings.TrimSpace(*in.Description)
			if len([]rune(description)) > MaxDescriptionChars {
				description = string([]rune(description)[:MaxDescriptionChars])
			}
			record.Description = description
		}
		if in.RedirectURIs != nil {
			redirects, err := ParseRedirectURIs(*in.RedirectURIs)
			if err != nil {
				return err
			}
			record.RedirectURIs = redirects
		}
		if in.Scopes != nil {
			record.Scopes = normaliseScopes(*in.Scopes)
		}
		if in.Trusted != nil {
			record.Trusted = *in.Trusted
		}
		if in.Disabled != nil {
			record.Disabled = *in.Disabled
		}
		record.UpdatedAt = time.Now().UnixMilli()

		if _, err := tx.Exec(ctx, `UPDATE oauth_apps
			SET name = ?, description = ?, redirect_uris = ?, scopes = ?,
			    trusted = ?, disabled = ?, updated_at = ?
			WHERE id = ?`,
			record.Name, record.Description, strings.Join(record.RedirectURIs, "\n"),
			strings.Join(record.Scopes, " "), record.Trusted, record.Disabled,
			record.UpdatedAt, record.ID); err != nil {
			return fmt.Errorf("idp: update application: %w", err)
		}
		return nil
	})
	if err != nil {
		return App{}, err
	}
	return record, nil
}

// RotateSecret issues a new secret and invalidates the old one. Everything
// already issued keeps working: the secret authenticates the application at
// the token endpoint, and a token that has already been handed over was
// authenticated when it was issued. A trusted application's secret is the
// super administrator's to rotate, because whoever holds it can exchange the
// codes its users are sent.
func (s *Store) RotateSecret(ctx context.Context, appID string, superAdmin bool) (string, error) {
	var secret string
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockApp(ctx, tx, appID); err != nil {
			return err
		}
		record, err := s.AppByID(ctx, tx, appID)
		if err != nil {
			return err
		}
		if !superAdmin && record.Trusted {
			return ErrTrustedApplication
		}
		if !record.Confidential {
			return ErrInvalidRequest
		}
		secret = id.Secret(secretBytes)
		if _, err := tx.Exec(ctx,
			`UPDATE oauth_apps SET secret_hash = ?, updated_at = ? WHERE id = ?`,
			Digest(secret), time.Now().UnixMilli(), appID); err != nil {
			return fmt.Errorf("idp: rotate secret: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

// DeleteApp removes an application and everything issued through it. A trusted
// one is the super administrator's to remove, for the same reason it is theirs
// to change.
func (s *Store) DeleteApp(ctx context.Context, appID string, superAdmin bool) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockApp(ctx, tx, appID); err != nil {
			return err
		}
		record, err := s.AppByID(ctx, tx, appID)
		if err != nil {
			return err
		}
		if !superAdmin && record.Trusted {
			return ErrTrustedApplication
		}
		if _, err := tx.Exec(ctx, `DELETE FROM oauth_apps WHERE id = ?`, appID); err != nil {
			return fmt.Errorf("idp: delete application: %w", err)
		}
		return nil
	})
}

func (s *Store) ListApps(ctx context.Context) ([]App, error) {
	rows, err := s.db.Query(ctx, `SELECT `+appColumns+` FROM oauth_apps ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("idp: list applications: %w", err)
	}
	defer rows.Close()

	out := []App{}
	for rows.Next() {
		record, _, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("idp: list applications: %w", err)
	}
	return out, nil
}

func (s *Store) AppByID(ctx context.Context, q database.Queryer, appID string) (App, error) {
	record, _, err := scanApp(s.queryer(q).QueryRow(ctx,
		`SELECT `+appColumns+` FROM oauth_apps WHERE id = ?`, appID))
	return record, err
}

func (s *Store) AppByClientID(ctx context.Context, q database.Queryer, clientID string) (App, error) {
	record, _, err := scanApp(s.queryer(q).QueryRow(ctx,
		`SELECT `+appColumns+` FROM oauth_apps WHERE client_id = ?`, clientID))
	return record, err
}

// Authenticate resolves the credentials an application presents at the token
// endpoint.
//
// A public application authenticates by its client id alone, which is not
// authentication at all — that is what PKCE is for, and the caller checks it.
// A confidential one must present the secret, compared as a digest.
func (s *Store) Authenticate(ctx context.Context, clientID, secret string) (App, error) {
	record, hash, err := scanApp(s.db.QueryRow(ctx,
		`SELECT `+appColumns+` FROM oauth_apps WHERE client_id = ?`, clientID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return App{}, ErrClientAuth
		}
		return App{}, err
	}
	if record.Disabled {
		return App{}, ErrClientAuth
	}
	if !record.Confidential {
		return record, nil
	}
	if secret == "" || Digest(secret) != hash {
		return App{}, ErrClientAuth
	}
	return record, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanApp(row rowScanner) (App, string, error) {
	var (
		record    App
		redirects string
		scopes    string
		creator   sql.NullString
		hash      string
	)
	err := row.Scan(&record.ID, &record.ClientID, &record.Name, &record.Description,
		&redirects, &scopes, &record.Trusted, &record.Disabled, &creator,
		&record.CreatedAt, &record.UpdatedAt, &hash)
	if err != nil {
		if database.IsNotFound(err) {
			return App{}, "", ErrNotFound
		}
		return App{}, "", fmt.Errorf("idp: read application: %w", err)
	}
	record.CreatedBy = creator.String
	record.Confidential = hash != ""
	record.RedirectURIs = splitLines(redirects)
	record.Scopes = ParseScopes(scopes)
	return record, hash, nil
}

// --- consent ------------------------------------------------------------------

// RecordGrant writes what an account has agreed to, widening what was there
// rather than replacing it: a second application asking for less does not
// take away what the first one was allowed.
func (s *Store) RecordGrant(ctx context.Context, q database.Queryer, appID, userID string, scopes []string) error {
	now := time.Now().UnixMilli()
	_, err := s.queryer(q).Exec(ctx,
		`INSERT INTO oauth_grants (id, app_id, user_id, scopes, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (app_id, user_id) DO UPDATE SET scopes = excluded.scopes, last_used_at = excluded.last_used_at`,
		id.New(), appID, userID, strings.Join(scopes, " "), now, now)
	if err != nil {
		return fmt.Errorf("idp: record consent: %w", err)
	}
	return nil
}

// GrantedScopes is what this account has already agreed to let this
// application see. Absent is not an error: it is somebody arriving for the
// first time.
func (s *Store) GrantedScopes(ctx context.Context, q database.Queryer, appID, userID string) ([]string, error) {
	var scopes string
	err := s.queryer(q).QueryRow(ctx,
		`SELECT scopes FROM oauth_grants WHERE app_id = ? AND user_id = ?`, appID, userID).Scan(&scopes)
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("idp: read consent: %w", err)
	}
	return ParseScopes(scopes), nil
}

// GrantsFor is the list an account reads in its own settings: which
// applications it has let in, and when.
func (s *Store) GrantsFor(ctx context.Context, userID string) ([]Grant, error) {
	rows, err := s.db.Query(ctx,
		`SELECT g.app_id, a.client_id, a.name, g.scopes, g.created_at, g.last_used_at
		 FROM oauth_grants g JOIN oauth_apps a ON a.id = g.app_id
		 WHERE g.user_id = ? ORDER BY g.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("idp: list authorisations: %w", err)
	}
	defer rows.Close()

	out := []Grant{}
	for rows.Next() {
		var (
			item   Grant
			scopes string
		)
		if err := rows.Scan(&item.AppID, &item.ClientID, &item.Name, &scopes,
			&item.CreatedAt, &item.LastUsedAt); err != nil {
			return nil, fmt.Errorf("idp: scan authorisation: %w", err)
		}
		item.Scopes = ParseScopes(scopes)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("idp: list authorisations: %w", err)
	}
	return out, nil
}

// RevokeGrant withdraws consent and everything issued under it, in one
// transaction. Leaving the tokens behind would make "remove this application"
// a button that removes it from a list and from nothing else.
func (s *Store) RevokeGrant(ctx context.Context, userID, appID string) (bool, error) {
	removed := false
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		result, err := tx.Exec(ctx,
			`DELETE FROM oauth_grants WHERE user_id = ? AND app_id = ?`, userID, appID)
		if err != nil {
			return fmt.Errorf("idp: revoke consent: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("idp: revoke consent: %w", err)
		}
		removed = affected > 0
		if _, err := tx.Exec(ctx,
			`UPDATE oauth_tokens SET revoked = ? WHERE user_id = ? AND app_id = ?`,
			true, userID, appID); err != nil {
			return fmt.Errorf("idp: revoke tokens: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM oauth_codes WHERE user_id = ? AND app_id = ?`, userID, appID); err != nil {
			return fmt.Errorf("idp: drop codes: %w", err)
		}
		return nil
	})
	return removed, err
}

// --- authorisation codes ------------------------------------------------------

type Code struct {
	AppID           string
	UserID          string
	RedirectURI     string
	Scopes          []string
	Nonce           string
	CodeChallenge   string
	ChallengeMethod string
}

func (s *Store) SaveCode(ctx context.Context, code string, in Code) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `INSERT INTO oauth_codes
		(code_hash, app_id, user_id, redirect_uri, scopes, nonce,
		 code_challenge, challenge_method, expires_at, used, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		Digest(code), in.AppID, in.UserID, in.RedirectURI, strings.Join(in.Scopes, " "),
		in.Nonce, in.CodeChallenge, in.ChallengeMethod,
		now.Add(CodeTTL).UnixMilli(), false, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("idp: save code: %w", err)
	}
	return nil
}

// RedeemCode spends a code exactly once.
//
// The spending is the UPDATE, not the read: marking it used with the old
// value in the WHERE clause is a compare-and-set that both engines make
// atomic, so two exchanges arriving together cannot both find it unused. A
// read followed by a write would let both through, and the whole point of a
// code is that it is worth one token pair.
//
// A code presented a second time is a code somebody else has a copy of, so
// everything this application holds for this account is revoked — the first
// exchange's tokens included. That is the specification's advice and it is
// also the only safe reading: one of the two presentations was not the
// application.
func (s *Store) RedeemCode(ctx context.Context, code string) (Code, error) {
	hash := Digest(code)
	var (
		out    Code
		replay struct{ appID, userID string }
	)

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		result, err := tx.Exec(ctx,
			`UPDATE oauth_codes SET used = ? WHERE code_hash = ? AND used = ? AND expires_at > ?`,
			true, hash, false, time.Now().UnixMilli())
		if err != nil {
			return fmt.Errorf("idp: spend code: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("idp: spend code: %w", err)
		}
		if affected == 0 {
			// Unknown, expired, or already spent. Only the last of those is
			// worth acting on, and it is the one worth acting on hard — but
			// not from in here: this transaction is about to be rolled back
			// with the error below, and a revocation written inside it would
			// be rolled back with everything else.
			var (
				appID  string
				userID string
				used   bool
			)
			readErr := tx.QueryRow(ctx,
				`SELECT app_id, user_id, used FROM oauth_codes WHERE code_hash = ?`, hash).
				Scan(&appID, &userID, &used)
			if readErr == nil && used {
				replay.appID, replay.userID = appID, userID
			}
			return ErrBadCode
		}

		var scopes string
		if err := tx.QueryRow(ctx,
			`SELECT app_id, user_id, redirect_uri, scopes, nonce, code_challenge, challenge_method
			 FROM oauth_codes WHERE code_hash = ?`, hash).
			Scan(&out.AppID, &out.UserID, &out.RedirectURI, &scopes, &out.Nonce,
				&out.CodeChallenge, &out.ChallengeMethod); err != nil {
			return fmt.Errorf("idp: read code: %w", err)
		}
		out.Scopes = ParseScopes(scopes)
		return nil
	})
	if err != nil {
		if replay.appID != "" {
			// One of the two presentations was not the application. Which one
			// cannot be known, so everything it holds for this account goes.
			if _, revokeErr := s.db.Exec(ctx,
				`UPDATE oauth_tokens SET revoked = ? WHERE app_id = ? AND user_id = ?`,
				true, replay.appID, replay.userID); revokeErr != nil {
				return Code{}, fmt.Errorf("idp: revoke after replay: %w", revokeErr)
			}
		}
		return Code{}, err
	}
	return out, nil
}

// --- tokens -------------------------------------------------------------------

type Token struct {
	ID         string
	AppID      string
	UserID     string
	Scopes     []string
	ExpiresAt  int64
	LastUsedAt int64
}

func (s *Store) SaveToken(ctx context.Context, q database.Queryer,
	access, refresh, appID, userID string, scopes []string,
) (Token, error) {
	now := time.Now()
	record := Token{
		ID:        id.New(),
		AppID:     appID,
		UserID:    userID,
		Scopes:    scopes,
		ExpiresAt: now.Add(TokenTTL).UnixMilli(),
	}
	refreshHash := ""
	refreshExpiry := int64(0)
	if refresh != "" {
		refreshHash = Digest(refresh)
		refreshExpiry = now.Add(RefreshTTL).UnixMilli()
	}
	_, err := s.queryer(q).Exec(ctx, `INSERT INTO oauth_tokens
		(id, token_hash, refresh_hash, app_id, user_id, scopes,
		 expires_at, refresh_expires_at, revoked, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, Digest(access), refreshHash, appID, userID, strings.Join(scopes, " "),
		record.ExpiresAt, refreshExpiry, false, now.UnixMilli(), 0)
	if err != nil {
		return Token{}, fmt.Errorf("idp: save token: %w", err)
	}
	return record, nil
}

// ResolveAccess finds a live access token. Expired, revoked and unknown are
// all the same answer: the caller is a stranger holding a string.
func (s *Store) ResolveAccess(ctx context.Context, token string) (Token, error) {
	var (
		record Token
		scopes string
	)
	err := s.db.QueryRow(ctx,
		`SELECT id, app_id, user_id, scopes, expires_at, last_used_at
		 FROM oauth_tokens
		 WHERE token_hash = ? AND revoked = ? AND expires_at > ?`,
		Digest(token), false, time.Now().UnixMilli()).
		Scan(&record.ID, &record.AppID, &record.UserID, &scopes,
			&record.ExpiresAt, &record.LastUsedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Token{}, ErrBadToken
		}
		return Token{}, fmt.Errorf("idp: read token: %w", err)
	}
	record.Scopes = ParseScopes(scopes)
	return record, nil
}

// Rotate spends a refresh token and issues a new pair in its place.
//
// Rotation rather than reuse, and the same compare-and-set the code uses: a
// refresh token that is presented twice has been copied, so the row is
// already gone by the time the second attempt arrives and everything issued
// to that application for that account is revoked.
func (s *Store) Rotate(ctx context.Context, refresh, access, newRefresh string) (Token, error) {
	hash := Digest(refresh)
	var (
		out    Token
		replay struct{ appID, userID string }
	)

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		var (
			previous Token
			scopes   string
		)
		err := tx.QueryRow(ctx,
			`SELECT id, app_id, user_id, scopes FROM oauth_tokens
			 WHERE refresh_hash = ? AND revoked = ? AND refresh_expires_at > ?`,
			hash, false, time.Now().UnixMilli()).
			Scan(&previous.ID, &previous.AppID, &previous.UserID, &scopes)
		if err != nil {
			if database.IsNotFound(err) {
				// Either it never existed, or it has already been rotated
				// away. The second is a copy in somebody else's hands, and it
				// is acted on after this transaction rather than inside it:
				// the error below rolls back everything written in here.
				var appID, userID string
				if found := tx.QueryRow(ctx,
					`SELECT app_id, user_id FROM oauth_tokens WHERE refresh_hash = ?`, hash).
					Scan(&appID, &userID); found == nil {
					replay.appID, replay.userID = appID, userID
				}
				return ErrBadToken
			}
			return fmt.Errorf("idp: read refresh token: %w", err)
		}

		// Spend it. The condition is what makes two simultaneous refreshes
		// resolve to one winner rather than two new pairs.
		result, err := tx.Exec(ctx,
			`UPDATE oauth_tokens SET revoked = ? WHERE id = ? AND revoked = ?`,
			true, previous.ID, false)
		if err != nil {
			return fmt.Errorf("idp: spend refresh token: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return ErrBadToken
		}

		out, err = s.SaveToken(ctx, tx, access, newRefresh,
			previous.AppID, previous.UserID, ParseScopes(scopes))
		return err
	})
	if err != nil {
		if replay.appID != "" {
			if _, revokeErr := s.db.Exec(ctx,
				`UPDATE oauth_tokens SET revoked = ? WHERE app_id = ? AND user_id = ?`,
				true, replay.appID, replay.userID); revokeErr != nil {
				return Token{}, fmt.Errorf("idp: revoke after replay: %w", revokeErr)
			}
		}
		return Token{}, err
	}
	return out, nil
}

// Revoke takes back whichever half was presented, along with its other half.
// Answering the same way for an unknown token is deliberate: the endpoint is
// public, and a caller must not be able to use it to test whether a string is
// a token somebody holds.
func (s *Store) Revoke(ctx context.Context, appID, token string) error {
	hash := Digest(token)
	_, err := s.db.Exec(ctx,
		`UPDATE oauth_tokens SET revoked = ?
		 WHERE app_id = ? AND (token_hash = ? OR refresh_hash = ?)`,
		true, appID, hash, hash)
	if err != nil {
		return fmt.Errorf("idp: revoke token: %w", err)
	}
	return nil
}

// Touch records that a token was used, and keeps the consent row's own
// "last used" in step so the list in an account's settings is about the
// application rather than about the token that happens to be current.
func (s *Store) Touch(ctx context.Context, tokenID, appID, userID string) {
	now := time.Now().UnixMilli()
	// Best effort. A failed bookkeeping write must not turn a valid identity
	// lookup into an error.
	_, _ = s.db.Exec(ctx, `UPDATE oauth_tokens SET last_used_at = ? WHERE id = ?`, now, tokenID)
	_, _ = s.db.Exec(ctx,
		`UPDATE oauth_grants SET last_used_at = ? WHERE app_id = ? AND user_id = ?`,
		now, appID, userID)
}

// Purge drops what is spent or expired. Called by the janitor: codes live two
// minutes and tokens an hour, so without it the two tables grow forever with
// rows nothing will ever read again.
func (s *Store) Purge(ctx context.Context) (int64, error) {
	now := time.Now().UnixMilli()
	var total int64
	result, err := s.db.Exec(ctx, `DELETE FROM oauth_codes WHERE expires_at < ?`, now)
	if err != nil {
		return 0, fmt.Errorf("idp: purge codes: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil {
		total += affected
	}
	// A token row is kept until its refresh half is past too, because that is
	// what the replay check reads.
	result, err = s.db.Exec(ctx,
		`DELETE FROM oauth_tokens WHERE expires_at < ? AND refresh_expires_at < ?`, now, now)
	if err != nil {
		return 0, fmt.Errorf("idp: purge tokens: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil {
		total += affected
	}
	return total, nil
}

// --- helpers ------------------------------------------------------------------

func normaliseScopes(scopes []string) []string {
	parsed := ParseScopes(strings.Join(scopes, " "))
	if len(parsed) == 0 {
		return append([]string(nil), DefaultScopes...)
	}
	// Every request here is an identity request, so the subject is always
	// part of it — an application configured without it would produce tokens
	// with nothing in them.
	if !Covers(parsed, []string{ScopeOpenID}) {
		parsed = ParseScopes(strings.Join(append(parsed, ScopeOpenID), " "))
	}
	return parsed
}

func splitLines(raw string) []string {
	out := []string{}
	for _, line := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
