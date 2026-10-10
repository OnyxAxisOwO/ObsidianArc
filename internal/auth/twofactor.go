package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/qr"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Two-step sign-in: a password, then a code from an authenticator app.
//
// The second step is a session state rather than a second credential store.
// A right password on an account that asks for a code buys a pending session
// — a row in the same table, with the same cookie, living ten minutes — and
// Attach does not treat it as signed in. The code turns it into a real one.
// Doing it this way means a provider sign-in, which arrives by redirect with
// no form to put a code field on, gets the second step for free: it lands on
// the same pending state and the sign-in page picks it up from there.
//
// Whether an account must have the second step is the operator's policy, and
// it is evaluated per request against the account as it is now, never
// stamped onto a session. An operator who switches the policy on reaches
// sessions that are already open, the same way disabling an account does.

var (
	ErrTwoFactorUnavailable = errors.New("auth: two-step sign-in needs an instance secret")
	ErrTwoFactorEnabled     = errors.New("auth: two-step sign-in is already on")
	ErrTwoFactorDisabled    = errors.New("auth: two-step sign-in is not on")
	ErrTwoFactorNoSetup     = errors.New("auth: there is no setup in progress")
	ErrTwoFactorCode        = errors.New("auth: that code is not valid")
	ErrTwoFactorMandatory   = errors.New("auth: two-step sign-in is required for this account")
	ErrNoPendingSignIn      = errors.New("auth: no sign-in is waiting for a code")
	// Authenticate's answer for a session that has not finished signing in.
	// An error rather than a flag on the session, so a caller that forgets to
	// look cannot mistake half a sign-in for a whole one.
	ErrSignInIncomplete = errors.New("auth: this sign-in is waiting for a code")
)

// SecondFactorRequired is how Login and StartSession say "right password,
// now the code". The token is the pending session's, for the cookie; it opens
// nothing but the second step.
type SecondFactorRequired struct{ Token string }

func (e *SecondFactorRequired) Error() string { return "auth: a second sign-in step is required" }

const (
	// Long enough to find a phone and open an app, short enough that a
	// half-signed-in browser left open does not stay one.
	pendingSignInTTL = 10 * time.Minute
	// How long a secret handed out for scanning may be confirmed. The
	// wizard asks for the code on the same screen, so this is generous.
	setupTTL = 30 * time.Minute
)

// TwoFactorEvent is a moment worth an entry in the security log. Auth does
// not know that log exists; the wiring does, and records what it is handed.
type TwoFactorEvent struct {
	// enabled, disabled, recovery_used or recovery_regenerated.
	Kind    string
	Account user.User
	IP      string
}

func (s *Service) twoFactorEvent(ctx context.Context, kind string, account user.User, ip string) {
	if s.OnTwoFactor == nil {
		return
	}
	// Detached and bounded: the change is committed, and an audit write that
	// failed or hung must not turn it into a failure the person sees.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.OnTwoFactor(recordCtx, TwoFactorEvent{Kind: kind, Account: account, IP: ip})
}

// --- policy -------------------------------------------------------------------

// TwoFactorPolicy is the operator's setting, read as optional when it is
// anything this build does not know. An unknown value failing open is the
// lesser evil: failing closed would lock every account out of an instance
// over a typo in an imported settings file.
func (s *Service) TwoFactorPolicy() string {
	policy := s.settings.Get(settings.TwoFactorPolicy)
	if !settings.ValidTwoFactorPolicy(policy) {
		return settings.TwoFactorOptional
	}
	return policy
}

// TwoFactorMandatory reports whether the policy covers this account, which is
// also whether it may switch the second step off again.
func (s *Service) TwoFactorMandatory(account user.User) bool {
	switch s.TwoFactorPolicy() {
	case settings.TwoFactorEveryone:
		return true
	case settings.TwoFactorAdmins, settings.TwoFactorBackoffice:
		return account.IsAdmin()
	}
	// A code on every entry to the backoffice needs a factor to take it from.
	return account.IsAdmin() && s.BackofficeVerifyOn()
}

// MustEnrolTwoFactor reports whether this account may do nothing until it
// has switched the second step on.
func (s *Service) MustEnrolTwoFactor(account user.User) bool {
	if account.TwoFactorEnabled() {
		return false
	}
	switch s.TwoFactorPolicy() {
	case settings.TwoFactorEveryone:
		return true
	case settings.TwoFactorAdmins:
		return account.IsAdmin()
	}
	return false
}

// BackofficeNeedsTwoFactor reports whether the backoffice refuses this
// account until it enrols. Every level above optional includes it, and so
// does asking for a code on every entry — there is nothing to ask for until
// the administrator has a factor.
func (s *Service) BackofficeNeedsTwoFactor(account user.User) bool {
	return account.IsAdmin() && !account.TwoFactorEnabled() &&
		(s.TwoFactorPolicy() != settings.TwoFactorOptional || s.BackofficeVerifyOn())
}

// --- a code at the backoffice's door ------------------------------------------
//
// Signing in proves who somebody is; this proves they are still the one at
// the keyboard when they reach for the pages and commands that can change
// everybody else's account. How often it asks is the operator's mode:
//
//   visit     every visit — leaving ends it, and so do the idle minutes
//   idle      working keeps it open; the idle minutes close it
//   interval  one code is good for the minutes after it, whatever happens
//
// A visit is held by whatever made the request. A browser session holds it
// in the database, shared by the backoffice pages, the web terminal and the
// chat's tools, because those are one person at one browser. An SSH
// connection holds its own in memory and loses it when it hangs up. Anything
// that holds neither is refused: a request nobody can prove a code for is
// not one that has proved one.

// BackofficeVerifyMode is the operator's mode, off for anything unknown for
// the reason TwoFactorPolicy gives.
func (s *Service) BackofficeVerifyMode() string {
	mode := s.settings.Get(settings.TwoFactorBackofficeMode)
	if !settings.ValidBackofficeVerifyMode(mode) {
		return settings.BackofficeVerifyOff
	}
	return mode
}

// BackofficeVerifyOn reports whether the backoffice asks for a code of its own.
func (s *Service) BackofficeVerifyOn() bool {
	return s.BackofficeVerifyMode() != settings.BackofficeVerifyOff
}

// BackofficeVerifies reports whether that applies to this account.
func (s *Service) BackofficeVerifies(account user.User) bool {
	return account.IsAdmin() && s.BackofficeVerifyOn()
}

// BackofficeMinutes is how long the mode's clock runs.
func (s *Service) BackofficeMinutes() int {
	minutes := s.settings.Int(settings.TwoFactorBackofficeMinutes, 15)
	return min(max(minutes, 1), settings.MaxTwoFactorBackofficeMinutes)
}

// backofficeGrant is a visit held by an SSH connection. In memory because it
// is exactly as durable as the connection holding it.
type backofficeGrant struct {
	mu sync.Mutex
	at int64
}

type backofficeGrantKey struct{}

// WithBackofficeGrant gives a context — one SSH connection's — a visit of its
// own to hold, starting closed.
func WithBackofficeGrant(ctx context.Context) context.Context {
	return context.WithValue(ctx, backofficeGrantKey{}, &backofficeGrant{})
}

func grantFrom(ctx context.Context) (*backofficeGrant, bool) {
	grant, ok := ctx.Value(backofficeGrantKey{}).(*backofficeGrant)
	return grant, ok
}

// backofficeAt is when the visit this request belongs to was last proved or,
// in the sliding modes, last used. False when nothing in the request can hold
// a visit at all.
func backofficeAt(ctx context.Context) (int64, bool) {
	if session, ok := SessionFrom(ctx); ok {
		return session.BackofficeAt, true
	}
	if grant, ok := grantFrom(ctx); ok {
		grant.mu.Lock()
		defer grant.mu.Unlock()
		return grant.at, true
	}
	return 0, false
}

// BackofficeLocked reports whether this request has to prove a code before
// the backoffice will answer it.
func (s *Service) BackofficeLocked(ctx context.Context, account user.User) bool {
	// Without a factor there is no code to ask for; BackofficeNeedsTwoFactor
	// refuses such an administrator before this is reached.
	if !s.BackofficeVerifies(account) || !account.TwoFactorEnabled() {
		return false
	}
	at, ok := backofficeAt(ctx)
	if !ok {
		return true
	}
	return time.Since(time.UnixMilli(at)) > time.Duration(s.BackofficeMinutes())*time.Minute
}

// KeepBackofficeOpen slides a visit forward while it is being used, in the
// two modes whose minutes count idleness. At most every half minute for a
// browser session: a page of the backoffice makes several requests at once,
// and each would otherwise be a write.
func (s *Service) KeepBackofficeOpen(ctx context.Context, account user.User) {
	if !s.BackofficeVerifies(account) || s.BackofficeVerifyMode() == settings.BackofficeVerifyInterval {
		return
	}
	now := time.Now()
	if session, ok := SessionFrom(ctx); ok {
		if session.BackofficeAt == 0 || now.Sub(time.UnixMilli(session.BackofficeAt)) < 30*time.Second {
			return
		}
		if err := s.sessions.SetBackofficeAt(ctx, session.ID, now.UnixMilli()); err != nil {
			slog.WarnContext(ctx, "could not extend a backoffice visit", "error", err)
		}
		return
	}
	if grant, ok := grantFrom(ctx); ok {
		grant.mu.Lock()
		if grant.at != 0 {
			grant.at = now.UnixMilli()
		}
		grant.mu.Unlock()
	}
}

// ErrNoBackofficeVisit is EnterBackoffice's answer for a request that has
// nothing to hold a visit in.
var ErrNoBackofficeVisit = errors.New("auth: this request cannot hold a backoffice visit")

// EnterBackoffice takes a code for the visit this request belongs to. Same
// secret, same replay guard, same guessing budget as signing in.
func (s *Service) EnterBackoffice(ctx context.Context, account user.User, code, ip, userAgent string) error {
	if !account.TwoFactorEnabled() {
		return ErrTwoFactorDisabled
	}
	session, hasSession := SessionFrom(ctx)
	grant, hasGrant := grantFrom(ctx)
	if !hasSession && !hasGrant {
		return ErrNoBackofficeVisit
	}
	if err := s.spendCode(ctx, account, code, ip); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if hasSession {
		return s.sessions.EnterBackofficeVisit(ctx, session.ID, now, ip, userAgent)
	}
	grant.mu.Lock()
	grant.at = now
	grant.mu.Unlock()
	return nil
}

// backofficeMoved reports whether a request no longer comes from where this
// session's visit was proved, by the operator's switches. Only a real browser
// request can be asked: a command the web terminal dispatches in-process has
// no address or browser of its own, which is why this runs in Attach.
func (s *Service) backofficeMoved(session Session, ip, userAgent string) bool {
	if session.BackofficeAt == 0 || !s.BackofficeVerifyOn() {
		return false
	}
	// A visit opened before visits were bound has nothing to compare with.
	// Ending it would lock the operator out the moment they switched the
	// binding on, over a difference that says nothing about who is asking.
	if session.BackofficeIP == "" && session.BackofficeUA == "" {
		return false
	}
	if s.settings.Bool(settings.TwoFactorBackofficeNetwork) && ip != session.BackofficeIP {
		return true
	}
	return s.settings.Bool(settings.TwoFactorBackofficeBrowser) &&
		text.Truncate(userAgent, MaxUserAgentChars) != session.BackofficeUA
}

// LeaveBackoffice ends this browser session's visit — in the one mode where
// leaving is what ends a visit. In the others coming and going is free, so
// the page can say it is leaving whatever the mode and this decides.
func (s *Service) LeaveBackoffice(ctx context.Context, session Session) error {
	if session.BackofficeAt == 0 || s.BackofficeVerifyMode() != settings.BackofficeVerifyVisit {
		return nil
	}
	return s.sessions.SetBackofficeAt(ctx, session.ID, 0)
}

// TwoFactorAvailable is false only on a build wired without an instance
// secret, which a real deployment never is: config generates one.
func (s *Service) TwoFactorAvailable() bool { return s.twoFactorBox != nil }

// TwoFactorIssuer is the name an authenticator app files the entry under.
func (s *Service) TwoFactorIssuer() string {
	for _, candidate := range []string{
		s.settings.Get(settings.TwoFactorIssuer),
		s.settings.Get(settings.SiteName),
	} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return settings.Defaults[settings.SiteName]
}

// RememberDays is how long a browser may skip the code, zero for never.
func (s *Service) RememberDays() int {
	days := s.settings.Int(settings.TwoFactorRememberDays, 0)
	return min(max(days, 0), settings.MaxTwoFactorRememberDays)
}

// --- the row ------------------------------------------------------------------

type twoFactorRow struct {
	secret    []byte
	pending   []byte
	pendingAt int64
	lastStep  int64
	recovery  []string
}

func (s *Service) readTwoFactor(ctx context.Context, q database.Queryer, userID string) (twoFactorRow, bool, error) {
	if q == nil {
		q = s.db
	}
	var (
		row      twoFactorRow
		recovery string
	)
	err := q.QueryRow(ctx,
		`SELECT secret, pending, pending_at, last_step, recovery FROM two_factor WHERE user_id = ?`,
		userID).Scan(&row.secret, &row.pending, &row.pendingAt, &row.lastStep, &recovery)
	if database.IsNotFound(err) {
		return twoFactorRow{}, false, nil
	}
	if err != nil {
		return twoFactorRow{}, false, fmt.Errorf("auth: read two-step row: %w", err)
	}
	if err := json.Unmarshal([]byte(recovery), &row.recovery); err != nil {
		return twoFactorRow{}, false, fmt.Errorf("auth: read recovery codes: %w", err)
	}
	return row, true, nil
}

// lockAccount is the per-account row lock AGENTS.md names: every read of the
// two-step row that decides a write holds it.
func lockAccount(ctx context.Context, tx *database.Tx, userID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("auth: lock account: %w", err)
	}
	return nil
}

// recoveryDigest keys the digest with a secret derived from the instance
// key. A plain hash of a fifty-bit code is within reach of a determined
// offline search; a keyed one is not, without the key a dump does not hold.
func (s *Service) recoveryDigest(normalized string) string {
	mac := hmac.New(sha256.New, s.twoFactorKey)
	mac.Write([]byte("recovery\x00" + normalized))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) recoveryDigests(codes []string) (string, error) {
	digests := make([]string, len(codes))
	for i, code := range codes {
		digests[i] = s.recoveryDigest(totp.NormalizeRecovery(code))
	}
	encoded, err := json.Marshal(digests)
	if err != nil {
		return "", fmt.Errorf("auth: encode recovery codes: %w", err)
	}
	return string(encoded), nil
}

// --- enrolling ----------------------------------------------------------------

// TwoFactorSetup is what the wizard draws: the picture to scan, and the same
// secret as text for when the camera will not cooperate.
type TwoFactorSetup struct {
	Secret  string `json:"secret"`
	URI     string `json:"uri"`
	Issuer  string `json:"issuer"`
	Account string `json:"account"`
	QR      struct {
		Size int    `json:"size"`
		Path string `json:"path"`
	} `json:"qr"`
	// Whether confirming will ask for the account's password, so the wizard
	// draws the field before the person has typed a code that would then be
	// refused for want of it.
	PasswordRequired bool `json:"password_required"`
}

// BeginTwoFactor hands out a fresh secret for scanning. Nothing changes for
// the account until EnableTwoFactor sees a code computed from it; asking
// again replaces the one waiting, so a wizard reopened after a lost phone
// never offers a secret that is already somewhere else.
func (s *Service) BeginTwoFactor(ctx context.Context, account user.User) (TwoFactorSetup, error) {
	if !s.TwoFactorAvailable() {
		return TwoFactorSetup{}, ErrTwoFactorUnavailable
	}
	if account.TwoFactorEnabled() {
		return TwoFactorSetup{}, ErrTwoFactorEnabled
	}
	hash, err := s.users.PasswordHash(ctx, nil, account.ID)
	if err != nil {
		return TwoFactorSetup{}, err
	}

	secretText := totp.NewSecret()
	sealed, err := s.twoFactorBox.Seal(secretText)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	now := time.Now().UnixMilli()
	// Only the pending columns: if a confirmation raced this and won, its
	// secret is left alone and this one simply goes unused.
	if _, err := s.db.Exec(ctx,
		`INSERT INTO two_factor (user_id, pending, pending_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET pending = excluded.pending,
		   pending_at = excluded.pending_at, updated_at = excluded.updated_at`,
		account.ID, sealed, now, now, now); err != nil {
		return TwoFactorSetup{}, fmt.Errorf("auth: store pending secret: %w", err)
	}

	setup := TwoFactorSetup{
		Secret:           secretText,
		Issuer:           s.TwoFactorIssuer(),
		Account:          account.Username,
		PasswordRequired: hash != "",
	}
	setup.URI = totp.URI(setup.Issuer, setup.Account, secretText)
	code, err := qr.Encode([]byte(setup.URI))
	if err != nil {
		return TwoFactorSetup{}, fmt.Errorf("auth: draw the QR code: %w", err)
	}
	setup.QR.Size = code.Size
	setup.QR.Path = code.Path()
	return setup, nil
}

// EnableTwoFactor confirms the secret handed out by BeginTwoFactor with a
// code from it, and switches the second step on. It returns the recovery
// codes, which exist in plain text for this one response and never again.
//
// Every other session on the account is signed out: none of them proved the
// second factor, and a stolen one should not outlive the lock that was just
// fitted because somebody suspected it.
//
// That sign-out is also why it takes the account's password, where it has
// one. A session is all a stolen cookie is; if it were enough, the thief
// would enrol their own authenticator, end the owner's sessions everywhere,
// and leave the owner unable to get back in.
func (s *Service) EnableTwoFactor(ctx context.Context, userID, password, code, keepSessionID, ip, userAgent string) ([]string, user.User, error) {
	if !s.TwoFactorAvailable() {
		return nil, user.User{}, ErrTwoFactorUnavailable
	}
	// Before the transaction, because it is an Argon2 verification.
	if err := s.ConfirmPassword(ctx, userID, password); err != nil {
		return nil, user.User{}, err
	}
	codes := totp.RecoveryCodes()
	digests, err := s.recoveryDigests(codes)
	if err != nil {
		return nil, user.User{}, err
	}

	var updated user.User
	now := time.Now()
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAccount(ctx, tx, userID); err != nil {
			return err
		}
		account, err := s.users.ByID(ctx, tx, userID)
		if err != nil {
			return err
		}
		if account.TwoFactorEnabled() {
			return ErrTwoFactorEnabled
		}
		row, found, err := s.readTwoFactor(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !found || len(row.pending) == 0 ||
			now.Sub(time.UnixMilli(row.pendingAt)) > setupTTL {
			return ErrTwoFactorNoSetup
		}
		secretText, err := s.twoFactorBox.Open(row.pending)
		if err != nil {
			return err
		}
		step, ok, err := totp.Match(secretText, code, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrTwoFactorCode
		}

		// The confirming code counts as used, so it cannot also be the one
		// typed at the next sign-in.
		if _, err := tx.Exec(ctx,
			`UPDATE two_factor SET secret = ?, pending = NULL, pending_at = 0, last_step = ?,
			   recovery = ?, updated_at = ? WHERE user_id = ?`,
			row.pending, step, digests, now.UnixMilli(), userID); err != nil {
			return fmt.Errorf("auth: switch two-step on: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE users SET two_factor_at = ?, updated_at = ? WHERE id = ?`,
			now.UnixMilli(), now.UnixMilli(), userID); err != nil {
			return fmt.Errorf("auth: mark two-step on: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`,
			userID, keepSessionID); err != nil {
			return fmt.Errorf("auth: revoke other sessions: %w", err)
		}
		// The code just typed is as good a proof as the backoffice's door
		// asks for, so the session that typed it walks straight in rather
		// than being asked for another one a second later — bound to where
		// it typed it, as a visit through the door is, or the operator's
		// network and browser switches would end it on the next request.
		if _, err := tx.Exec(ctx,
			`UPDATE sessions SET backoffice_at = ?, backoffice_ip = ?, backoffice_ua = ? WHERE id = ?`,
			now.UnixMilli(), ip, text.Truncate(userAgent, MaxUserAgentChars), keepSessionID); err != nil {
			return fmt.Errorf("auth: open the backoffice visit: %w", err)
		}
		account.TwoFactorAt = now.UnixMilli()
		updated = account
		return nil
	})
	if err != nil {
		return nil, user.User{}, err
	}
	s.twoFactorEvent(ctx, "enabled", updated, ip)
	return codes, updated, nil
}

// --- proving it ---------------------------------------------------------------

// checkCode accepts a one-time code or a recovery code for an account whose
// second step is on, and spends what it accepted: a one-time code by moving
// the last-used step past it, a recovery code by removing it.
func (s *Service) checkCode(ctx context.Context, userID, candidate string) (string, error) {
	if !s.TwoFactorAvailable() {
		return "", ErrTwoFactorUnavailable
	}
	now := time.Now()

	if totp.LooksLikeCode(candidate) {
		row, found, err := s.readTwoFactor(ctx, nil, userID)
		if err != nil {
			return "", err
		}
		if !found || len(row.secret) == 0 {
			return "", ErrTwoFactorDisabled
		}
		secretText, err := s.twoFactorBox.Open(row.secret)
		if err != nil {
			return "", err
		}
		step, ok, err := totp.Match(secretText, candidate, now)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrTwoFactorCode
		}
		// The read above decides nothing on its own: this conditional write
		// is the check. Two requests carrying the same code both match, and
		// exactly one of them moves last_step; the other finds it already
		// there and is refused, on either engine and across processes.
		result, err := s.db.Exec(ctx,
			`UPDATE two_factor SET last_step = ?, updated_at = ?
			 WHERE user_id = ? AND last_step < ? AND secret IS NOT NULL`,
			step, now.UnixMilli(), userID, step)
		if err != nil {
			return "", fmt.Errorf("auth: spend code: %w", err)
		}
		if spent, err := result.RowsAffected(); err != nil || spent != 1 {
			return "", ErrTwoFactorCode
		}
		return "code", nil
	}

	normalized := totp.NormalizeRecovery(candidate)
	if normalized == "" {
		return "", ErrTwoFactorCode
	}
	digest := s.recoveryDigest(normalized)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAccount(ctx, tx, userID); err != nil {
			return err
		}
		row, found, err := s.readTwoFactor(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !found || len(row.secret) == 0 {
			return ErrTwoFactorDisabled
		}
		match := -1
		for i, stored := range row.recovery {
			if hmac.Equal([]byte(stored), []byte(digest)) {
				match = i
			}
		}
		if match < 0 {
			return ErrTwoFactorCode
		}
		remaining := append(append([]string{}, row.recovery[:match]...), row.recovery[match+1:]...)
		encoded, err := json.Marshal(remaining)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE two_factor SET recovery = ?, updated_at = ? WHERE user_id = ?`,
			string(encoded), now.UnixMilli(), userID)
		return err
	})
	if err != nil {
		return "", err
	}
	return "recovery", nil
}

// spendCode is checkCode behind the guessing limit. A code is six digits, so
// it is only as strong as the number of tries it allows; the limiter is the
// same one that makes a password slow to guess, keyed by the account so a
// botnet gains nothing by spreading out.
//
// Its own limiter rather than the password one: somebody who got the password
// right has not earned a clean slate for the code.
func (s *Service) spendCode(ctx context.Context, account user.User, candidate, ip string) error {
	attempt, err := s.codes.Begin(ip, account.ID)
	if err != nil {
		return err
	}
	defer attempt.finish(attemptCancelled)

	method, err := s.checkCode(ctx, account.ID, candidate)
	if errors.Is(err, ErrTwoFactorCode) {
		attempt.finish(attemptFailed)
		return err
	}
	if err != nil {
		return err
	}
	// Releases the reservation without forgiving the failures before it: a
	// guesser who is eventually right must not get a clean count back.
	attempt.finish(attemptCancelled)
	if method == "recovery" {
		s.twoFactorEvent(ctx, "recovery_used", account, ip)
	}
	return nil
}

// VerifyTwoFactorCode is the second step for a transport that has no session
// to promote — the SSH console, which has already checked the password.
func (s *Service) VerifyTwoFactorCode(ctx context.Context, account user.User, code, ip string) error {
	if !account.TwoFactorEnabled() {
		return ErrTwoFactorDisabled
	}
	return s.spendCode(ctx, account, code, ip)
}

// CompleteSignIn trades a pending session and a code for a real session.
// The pending row is deleted and a new token issued rather than the flag
// being flipped, so whatever saw the half-way cookie has nothing afterwards.
func (s *Service) CompleteSignIn(ctx context.Context, token, code, ip, ua string) (user.User, string, error) {
	if token == "" {
		return user.User{}, "", ErrNoPendingSignIn
	}
	session, account, err := s.sessions.GetWithUser(ctx, token)
	if errors.Is(err, ErrSessionNotFound) || (err == nil && !session.TwoFactorPending) {
		return user.User{}, "", ErrNoPendingSignIn
	}
	if err != nil {
		return user.User{}, "", err
	}
	if !account.IsActive() {
		return user.User{}, "", &AccountDisabledError{Reason: account.BanReason}
	}
	if !account.TwoFactorEnabled() {
		// Switched off by an administrator while this sign-in was waiting.
		// The code it was waiting for no longer exists; starting again is
		// the honest answer, and costs one password.
		_ = s.sessions.DeleteByID(ctx, session.ID)
		return user.User{}, "", ErrNoPendingSignIn
	}

	if err := s.spendCode(ctx, account, code, ip); err != nil {
		return user.User{}, "", err
	}

	if err := s.sessions.DeleteByID(ctx, session.ID); err != nil {
		return user.User{}, "", err
	}
	full, _, err := s.sessions.Create(ctx, account.ID, s.cfg.TTL, ip, ua)
	if err != nil {
		return user.User{}, "", err
	}
	now := time.Now().UnixMilli()
	_ = s.users.MarkLogin(ctx, account.ID, now)
	account.LastLoginAt = now
	return account, full, nil
}

// secondStep decides what a proven first factor buys: the whole session, or
// the pending half and a request for the code.
func (s *Service) secondStep(ctx context.Context, account user.User, remembered, ip, ua string) (string, error) {
	if account.TwoFactorEnabled() && !s.rememberedBrowser(remembered, account) {
		token, _, err := s.sessions.CreatePending(ctx, account.ID, pendingSignInTTL, ip, ua)
		if err != nil {
			return "", err
		}
		return "", &SecondFactorRequired{Token: token}
	}
	token, _, err := s.sessions.Create(ctx, account.ID, s.cfg.TTL, ip, ua)
	if err != nil {
		return "", err
	}
	_ = s.users.MarkLogin(ctx, account.ID, time.Now().UnixMilli())
	return token, nil
}

// --- switching it off and starting over ---------------------------------------

// DisableTwoFactor switches the second step off. It takes a code — a stolen
// session is exactly what this must not be enough for — and refuses outright
// where the operator's policy covers the account.
func (s *Service) DisableTwoFactor(ctx context.Context, account user.User, code, ip string) (user.User, error) {
	if !account.TwoFactorEnabled() {
		return user.User{}, ErrTwoFactorDisabled
	}
	if s.TwoFactorMandatory(account) {
		return user.User{}, ErrTwoFactorMandatory
	}
	if err := s.spendCode(ctx, account, code, ip); err != nil {
		return user.User{}, err
	}
	updated, err := s.clearTwoFactor(ctx, account.ID, nil)
	if err != nil {
		return user.User{}, err
	}
	s.twoFactorEvent(ctx, "disabled", updated, ip)
	return updated, nil
}

// ResetTwoFactor is the administrator's answer to a lost phone and a lost
// sheet of recovery codes. The authorisation runs inside the same lock role
// changes take, for the reason SetPassword gives: a reset must not race a
// promotion and quietly strip the lock off a newly privileged account.
func (s *Service) ResetTwoFactor(ctx context.Context, userID string, authorize func(database.Queryer, user.User) error) (user.User, error) {
	return s.clearTwoFactor(ctx, userID, authorize)
}

func (s *Service) clearTwoFactor(ctx context.Context, userID string, authorize func(database.Queryer, user.User) error) (user.User, error) {
	var updated user.User
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if authorize != nil {
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
		}
		if err := lockAccount(ctx, tx, userID); err != nil {
			return err
		}
		account, err := s.users.ByID(ctx, tx, userID)
		if err != nil {
			return err
		}
		if authorize != nil {
			if err := authorize(tx, account); err != nil {
				return err
			}
		}
		if !account.TwoFactorEnabled() {
			return ErrTwoFactorDisabled
		}
		if _, err := tx.Exec(ctx, `DELETE FROM two_factor WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("auth: remove two-step row: %w", err)
		}
		now := time.Now().UnixMilli()
		if _, err := tx.Exec(ctx, `UPDATE users SET two_factor_at = 0, updated_at = ? WHERE id = ?`,
			now, userID); err != nil {
			return fmt.Errorf("auth: mark two-step off: %w", err)
		}
		// A visit to the backoffice opened with the factor being removed was
		// proved with something that no longer exists.
		if _, err := tx.Exec(ctx, `UPDATE sessions SET backoffice_at = 0 WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("auth: close backoffice visits: %w", err)
		}
		account.TwoFactorAt = 0
		updated = account
		return nil
	})
	return updated, err
}

// RegenerateRecovery replaces every unused recovery code with a fresh set,
// for somebody who has used a few or cannot find the sheet. It takes a code,
// for the reason switching off does.
func (s *Service) RegenerateRecovery(ctx context.Context, account user.User, code, ip string) ([]string, error) {
	if !account.TwoFactorEnabled() {
		return nil, ErrTwoFactorDisabled
	}
	if err := s.spendCode(ctx, account, code, ip); err != nil {
		return nil, err
	}
	codes := totp.RecoveryCodes()
	digests, err := s.recoveryDigests(codes)
	if err != nil {
		return nil, err
	}
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAccount(ctx, tx, account.ID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx,
			`UPDATE two_factor SET recovery = ?, updated_at = ? WHERE user_id = ? AND secret IS NOT NULL`,
			digests, time.Now().UnixMilli(), account.ID)
		if err != nil {
			return fmt.Errorf("auth: replace recovery codes: %w", err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrTwoFactorDisabled
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.twoFactorEvent(ctx, "recovery_regenerated", account, ip)
	return codes, nil
}

// --- what the settings screen reads ------------------------------------------

type TwoFactorStatus struct {
	Available bool  `json:"available"`
	Enabled   bool  `json:"enabled"`
	EnabledAt int64 `json:"enabled_at"`
	// Unused recovery codes. The screen warns as this runs low.
	RecoveryRemaining int `json:"recovery_remaining"`
	// Whether the operator's policy covers this account — which is also
	// whether the screen offers a way to switch it off.
	Mandatory    bool   `json:"mandatory"`
	Policy       string `json:"policy"`
	RememberDays int    `json:"remember_days"`
}

func (s *Service) TwoFactorStatus(ctx context.Context, account user.User) (TwoFactorStatus, error) {
	status := TwoFactorStatus{
		Available:    s.TwoFactorAvailable(),
		Enabled:      account.TwoFactorEnabled(),
		EnabledAt:    account.TwoFactorAt,
		Mandatory:    s.TwoFactorMandatory(account),
		Policy:       s.TwoFactorPolicy(),
		RememberDays: s.RememberDays(),
	}
	if status.Enabled {
		row, found, err := s.readTwoFactor(ctx, nil, account.ID)
		if err != nil {
			return TwoFactorStatus{}, err
		}
		if found {
			status.RecoveryRemaining = len(row.recovery)
		}
	}
	return status, nil
}

// TwoFactorAdoption is how far an instance is from the policy it wants. The
// accounts named are the administrators still without it, because those are
// the ones a stricter policy would stop at the door.
type TwoFactorAdoption struct {
	Accounts        int64             `json:"accounts"`
	Enabled         int64             `json:"enabled"`
	Admins          int64             `json:"admins"`
	AdminsEnabled   int64             `json:"admins_enabled"`
	AdminsWithout   []TwoFactorMember `json:"admins_without"`
	Policy          string            `json:"policy"`
	RememberDays    int               `json:"remember_days"`
	IssuerFallback  string            `json:"issuer_fallback"`
	SecretAvailable bool              `json:"available"`
}

type TwoFactorMember struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
}

func (s *Service) TwoFactorAdoption(ctx context.Context) (TwoFactorAdoption, error) {
	out := TwoFactorAdoption{
		Policy:          s.TwoFactorPolicy(),
		RememberDays:    s.RememberDays(),
		IssuerFallback:  s.TwoFactorIssuer(),
		SecretAvailable: s.TwoFactorAvailable(),
		AdminsWithout:   []TwoFactorMember{},
	}
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*),
		   COALESCE(SUM(CASE WHEN two_factor_at > 0 THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN role <> ? THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN role <> ? AND two_factor_at > 0 THEN 1 ELSE 0 END), 0)
		 FROM users WHERE status = ?`,
		user.RoleUser, user.RoleUser, user.StatusActive).
		Scan(&out.Accounts, &out.Enabled, &out.Admins, &out.AdminsEnabled); err != nil {
		return TwoFactorAdoption{}, fmt.Errorf("auth: count two-step adoption: %w", err)
	}

	// Bounded: this is a to-do list for an operator, and one with more than
	// fifty administrators on it has a different problem.
	rows, err := s.db.Query(ctx,
		`SELECT id, username, nickname FROM users
		 WHERE status = ? AND role <> ? AND two_factor_at = 0
		 ORDER BY username_lower LIMIT 50`,
		user.StatusActive, user.RoleUser)
	if err != nil {
		return TwoFactorAdoption{}, fmt.Errorf("auth: list administrators without two-step: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var member TwoFactorMember
		if err := rows.Scan(&member.ID, &member.Username, &member.Nickname); err != nil {
			return TwoFactorAdoption{}, fmt.Errorf("auth: scan administrator: %w", err)
		}
		out.AdminsWithout = append(out.AdminsWithout, member)
	}
	return out, rows.Err()
}

// --- remembering a browser ----------------------------------------------------
//
// A signed cookie, not a table: it names the account, when it was issued and
// the moment the second step was switched on, and carries a MAC over all
// three. Switching the step off and on again moves that moment, so every
// browser remembered before stops being remembered without anything having
// to be found and deleted. The lifetime is checked against the setting as it
// is now, so an operator shortening it — or setting it to zero — reaches
// cookies already issued.

func (s *Service) rememberCookieName() string { return s.cfg.CookieName + "_2fa" }

func (s *Service) rememberSignature(userID string, issuedAt, epoch int64) string {
	mac := hmac.New(sha256.New, s.twoFactorKey)
	mac.Write([]byte("remember\x00" + userID + "\x00" +
		strconv.FormatInt(issuedAt, 10) + "\x00" + strconv.FormatInt(epoch, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// RememberedFrom reads the cookie off a request, or "" when there is none.
func (s *Service) RememberedFrom(r *http.Request) string {
	cookie, err := r.Cookie(s.rememberCookieName())
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Service) rememberedBrowser(value string, account user.User) bool {
	days := s.RememberDays()
	if value == "" || days <= 0 || !s.TwoFactorAvailable() || !account.TwoFactorEnabled() {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != account.ID {
		return false
	}
	issuedAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	want := s.rememberSignature(account.ID, issuedAt, account.TwoFactorAt)
	if !hmac.Equal([]byte(parts[2]), []byte(want)) {
		return false
	}
	issued := time.UnixMilli(issuedAt)
	return time.Now().Before(issued.Add(time.Duration(days)*24*time.Hour)) &&
		!issued.After(time.Now().Add(time.Minute))
}

// Remember sets the cookie after a code was accepted and asked to be
// remembered. It does nothing where the operator has not allowed it.
func (s *Service) Remember(w http.ResponseWriter, account user.User) {
	days := s.RememberDays()
	if days <= 0 || !s.TwoFactorAvailable() || !account.TwoFactorEnabled() {
		return
	}
	issuedAt := time.Now().UnixMilli()
	http.SetCookie(w, &http.Cookie{
		Name:     s.rememberCookieName(),
		Value:    account.ID + "." + strconv.FormatInt(issuedAt, 10) + "." + s.rememberSignature(account.ID, issuedAt, account.TwoFactorAt),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   days * 24 * 60 * 60,
	})
}

// --- the enrolment gate -------------------------------------------------------

// enrolmentAllowed is what an account the policy is holding at the door may
// still reach: enough to know who it is, to enrol, to keep its theme, and to
// leave.
func enrolmentAllowed(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /api/site", "GET /api/site/logo", "GET /api/health", "GET /api/auth/me", "POST /api/auth/logout",
		"GET /api/profile/two-factor", "POST /api/profile/two-factor/setup",
		"POST /api/profile/two-factor/enable",
		"GET /api/preferences", "PATCH /api/preferences", "GET /api/preferences/wallpaper":
		return true
	}
	return false
}

// EnrolmentGate holds an account the policy says must enrol to the enrolment
// endpoints, on the server, where a hidden button is not the control.
//
// It sits after Attach and covers /api/ and the one other path that acts on
// a session: /oauth/authorize, which would otherwise hand a code for this
// account to another site. The page shell and its assets pass, so the
// browser can load the screen that does the enrolling.
func (s *Service) EnrolmentGate() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			account, ok := UserFrom(r.Context())
			if !ok || !s.MustEnrolTwoFactor(account) || enrolmentAllowed(r) {
				next.ServeHTTP(w, r)
				return
			}
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/"):
				httpx.WriteError(w, r, httpx.ForbiddenCode("two_factor_enrolment_required",
					"Set up two-step sign-in before continuing."))
			case r.URL.Path == "/oauth/authorize":
				// Back here once enrolled, with the application's request
				// intact: it carries a state and a nonce that are not ours.
				http.Redirect(w, r, "/two-factor?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
