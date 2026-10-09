// Package invite owns invite codes: an administrator's batch, an account's
// own personal code, and the one atomic operation both are spent through.
//
// Two shapes of code share one table rather than two, because the thing that
// has to be exactly right — consuming a code exactly once under concurrent
// registrations — is one statement, and a second table would be a second
// copy of it to keep honest. What differs between an admin batch and a
// personal code is only which columns are set: owner_id, a group and its
// trial length for the former; nothing but an unlimited max_uses for the
// latter.
package invite

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"regexp"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Status is computed from a code's own columns at read time rather than
// stored: it is a pure function of revoked_at, expires_at, max_uses and uses,
// and a stored copy would just be a second value that could disagree with
// the columns that actually govern what Consume does.
const (
	StatusActive  = "active"
	StatusUsedUp  = "used_up"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
)

func status(revokedAt, expiresAt int64, maxUses, uses int, now int64) string {
	switch {
	case revokedAt != 0:
		return StatusRevoked
	case expiresAt != 0 && expiresAt <= now:
		return StatusExpired
	case maxUses != 0 && uses >= maxUses:
		return StatusUsedUp
	default:
		return StatusActive
	}
}

// Code is one row, as an administrator or an owning account sees it.
type Code struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	OwnerID       string `json:"owner_id"`
	OwnerUsername string `json:"owner_username"`
	OwnerNickname string `json:"owner_nickname"`
	// What this row is for — see the CodeKind constants. Independent of
	// OwnerID, which only ever distinguishes "an account's own" from
	// "nobody's": a batch and a partner code are both owner_id = '', and
	// only Kind tells them apart.
	Kind string `json:"kind"`
	// The partner's name. Only ever set for a partner code — see Create.
	Name string `json:"name"`
	// Whether an account that already exists may spend this code through
	// Claim, on top of anyone who registers through it.
	AllowExisting bool   `json:"allow_existing"`
	GroupID       string `json:"group_id"`
	GroupName     string `json:"group_name"`
	GroupDays     int    `json:"group_days"`
	GroupDaysMax  int    `json:"group_days_max"`
	MaxUses       int    `json:"max_uses"`
	Uses          int    `json:"uses"`
	ExpiresAt     int64  `json:"expires_at"`
	RevokedAt     int64  `json:"revoked_at"`
	Note          string `json:"note"`
	CreatedBy     string `json:"created_by"`
	CreatedAt     int64  `json:"created_at"`
	Status        string `json:"status"`
	// How many invite_claims rows name this code — an existing account
	// having redeemed it through Claim, distinct from Uses, which counts
	// registrations. Populated by List and ByID; zero otherwise.
	Claims int `json:"claims"`
}

// CodeKind is what a code is for, stored in the kind column. OwnerID alone
// cannot tell a batch from a partner code — both are issued by an
// administrator with owner_id = ” — so this is the one column both List's
// kind filter and Claim's "is this claimable at all" check actually read.
const (
	CodeKindBatch    = "batch"
	CodeKindPartner  = "partner"
	CodeKindPersonal = "personal"
)

// Use is one registration through a code, as an owner or an administrator
// reads it back. Reward is spelled as two fields rather than an enum: a
// reader needs "did this count" (rewarded) and, when it did not, why
// (reward_skipped) — collapsing those into one value would make the zero
// case ("not resolved yet", still possible right after registration when
// verification is required) indistinguishable from "resolved, not rewarded".
type Use struct {
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	Nickname      string `json:"nickname"`
	GroupDays     int64  `json:"group_days"`
	CreatedAt     int64  `json:"created_at"`
	RewardedAt    int64  `json:"rewarded_at"`
	RewardCards   int    `json:"reward_cards"`
	RewardSkipped string `json:"reward_skipped"`
	// "register" for a row from invite_uses (a new account seated by this
	// code) or "claim" for one from invite_claims (an existing account that
	// redeemed it through Claim). The two tables are unioned for this list —
	// see Uses — so a reader needs a way to tell which one a row came from;
	// a claim has no reward fields worth reading, which is why they are
	// zero rather than absent.
	Via string `json:"via"`
}

const (
	ViaRegister = "register"
	ViaClaim    = "claim"
)

var (
	ErrNotFound         = errors.New("invite: no such code")
	ErrInvalid          = errors.New("invite: that code is not valid")
	ErrCodeTaken        = errors.New("invite: that code already exists")
	ErrCodeFormat       = errors.New("invite: a custom code must be 4-32 characters of A-Z and 0-9")
	ErrNamedBatch       = errors.New("invite: a batch is generated, so it cannot be given a code of its own")
	ErrInvalidCount     = errors.New("invite: count must be between 1 and 500")
	ErrInvalidMaxUses   = errors.New("invite: max uses must be between 0 and 100000")
	ErrInvalidGroupDays = errors.New("invite: group days must be between 0 and 3650, and group_days_max must be 0 or at least group_days")
	ErrGroupRequired    = errors.New("invite: group_days_max requires a group")
	// A partner code's own four required fields — see Create. One error for
	// all of them, the same reasoning ErrInvalid collapses a guessed code's
	// every failure mode into one answer: which field was missing is not
	// something the form has to guess at either, since it is the one that
	// sent the request.
	ErrPartnerFields = errors.New("invite: a partner code requires a count of 1, a custom code, a name and a group")

	// Claim's own failures, distinct from Consume's ErrInvalid so the HTTP
	// layer can tell "this code will never work for you" (ErrInvalid) apart
	// from "you already used this one" and "your membership already
	// disagrees with this one" — both real states a signed-in account can
	// be shown, unlike a guess at registration, which gets one flat answer
	// on purpose.
	ErrClaimed       = errors.New("invite: this code has already been claimed")
	ErrGroupConflict = errors.New("invite: claiming this code would replace an existing group membership")
	// The creator of a code cannot claim it onto their own account. That is
	// a fact about the code, not a guess at it, so the refusal does not
	// charge the throttle, the same as a repeat claim.
	ErrOwnCode = errors.New("invite: you created this code, so it cannot be claimed on your own account")
)

const (
	MaxBatch     = 500
	MaxNoteChars = 200
	MaxGroupDays = 3650
	MaxMaxUses   = 100000
	// A generated code's own fixed length — see generatedAlphabet. A custom
	// one's 4-32 character bound lives only in customCodeRE below and in
	// ErrCodeFormat's message: nothing else in this package cares what the
	// bound actually is.
	generatedLen = 8
)

// generatedAlphabet excludes 0, 1, I, L, O and U — every character that can
// be misread for another on a screen or mistyped from a photo of one. Thirty
// symbols wide, so eight of them is close enough to the entropy a UUID's
// first segment carries that guessing one is not a realistic attack; it does
// not have to be a secret on the order of a password, only unguessable
// enough that grep-ing sequential codes is not a strategy.
const generatedAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

var customCodeRE = regexp.MustCompile(`^[A-Z0-9]{4,32}$`)

// Normalise is the one folding rule for a code, applied to what an
// administrator types, what a registration form sends and what is stored:
// upper-cased, with spaces and hyphens removed. "abcd-1234", "ABCD 1234" and
// "abcd1234" all name the same row because they all normalise to the same
// string.
func Normalise(raw string) string {
	var out strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if r == ' ' || r == '-' {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// ValidCustom reports whether a normalised string is an acceptable
// administrator-named code: 4-32 characters of A-Z and 0-9. Deliberately
// wider than the generated alphabet — a partner's own chosen word is not
// read off a screen the way a generated one is, so there is no reason to
// keep it off 0, 1, I, L, O or U.
func ValidCustom(normalised string) bool { return customCodeRE.MatchString(normalised) }

// Display puts the hyphen back for an eight-character code — the shape every
// generated code is — and returns anything else, a custom one, exactly as
// stored: an administrator who chose "PARTNERX" typed it without a hyphen
// and should see it without one.
func Display(code string) string {
	if len(code) == generatedLen {
		return code[:4] + "-" + code[4:]
	}
	return code
}

// generateCode returns something a person can read off a screen and type
// without asking which character that was — see generatedAlphabet.
func generateCode() (string, error) {
	picked, err := drawSymbols(rand.Reader, generatedLen, len(generatedAlphabet))
	if err != nil {
		return "", fmt.Errorf("invite: generate code: %w", err)
	}
	var out strings.Builder
	for _, symbol := range picked {
		out.WriteByte(generatedAlphabet[symbol])
	}
	return out.String(), nil
}

// codeGenerator is a variable so tests can simulate collisions or generator
// failures deterministically without relying on random draws.
var codeGenerator = generateCode

// drawSymbols returns count uniform indices into an alphabet width symbols
// wide. Rejection sampling rather than `int(b) % width`, the same reasoning
// card.drawSymbols carries: 256 is not a multiple of 30, so the naive
// spelling would favour the alphabet's first characters by a small but real
// margin in a value whose whole job is to be unguessable.
func drawSymbols(reader io.Reader, count, width int) ([]byte, error) {
	if width < 1 || width > 256 {
		return nil, fmt.Errorf("invite: alphabet width %d", width)
	}
	ceiling := 256 - 256%width

	picked := make([]byte, 0, count)
	batch := make([]byte, count)
	for len(picked) < count {
		if _, err := io.ReadFull(reader, batch); err != nil {
			return nil, err
		}
		for _, b := range batch {
			if int(b) >= ceiling {
				continue
			}
			picked = append(picked, byte(int(b)%width))
			if len(picked) == count {
				break
			}
		}
	}
	return picked, nil
}

// Store owns invite_codes, invite_uses, invite_claims and the claim
// throttle.
type Store struct {
	db       *database.DB
	users    *user.Store
	cards    *card.Store
	groups   *group.Store
	settings *settings.Service
	// Where a reward tells its inviter it landed. Set by the wiring, the
	// same seam every other store that raises a notice uses; nil means
	// "push nothing", which is every instance predating this feature and
	// every test with no reason to exercise it.
	Notify *notify.Store
}

func NewStore(db *database.DB, users *user.Store, cards *card.Store, groups *group.Store, set *settings.Service) *Store {
	return &Store{db: db, users: users, cards: cards, groups: groups, settings: set}
}

// Settings reads back the invite settings a caller building a response needs
// without reaching past this package into internal/settings' key names
// itself — the profile handler is the one call site, and this keeps the
// key constants from having to be exported knowledge outside this package
// and internal/admin.
func (s *Store) Settings() (userEnabled bool, limit, rewardCards, rewardCardDays, rewardEvery int) {
	rewardEvery = s.settings.Int(settings.InvitesRewardEvery, 1)
	if rewardEvery < 1 {
		// A cadence of zero or less would either divide by zero or reward
		// every invite forever regardless of what an operator typed; 1
		// reproduces the original always-pays behaviour, which is also the
		// column's own default.
		rewardEvery = 1
	}
	return s.settings.Bool(settings.InvitesUserEnabled),
		s.settings.Int(settings.InvitesUserLimit, 10),
		s.settings.Int(settings.InvitesRewardCards, 0),
		s.settings.Int(settings.InvitesRewardCardDays, 30),
		rewardEvery
}

// nextRewardIn is how many more qualifying invites the inviter needs before
// InvitesRewardCards next pays out — 0 while the reward is switched off
// entirely, since "3 more" would be a promise the instance is not making.
// Shared by the profile payload (the account's own count) and Reward's
// notification (the count a fresh invite just produced), so the two can
// never state the arithmetic differently.
func nextRewardIn(counted, every, rewardCards int) int {
	if rewardCards <= 0 {
		return 0
	}
	if every < 1 {
		every = 1
	}
	return every - (counted % every)
}

// Grant is what Consume hands back: which code was spent, who gets credited
// for the invite (empty for an admin-issued code), and the group membership
// it carries. auth.InviteGrant mirrors this shape — see server.go's wiring
// — rather than this package being imported by internal/auth: auth is
// imported back by this package's own HTTP handlers (auth.RequireUser), and
// a two-way import between them does not compile.
type Grant struct {
	CodeID    string
	Code      string
	OwnerID   string
	GroupID   string
	GroupDays int64
}

// Consume spends one use of a code atomically, inside the caller's
// transaction — the registration that is asking must commit or roll back
// together with the use it spends, so a registration that fails for any
// other reason (a taken username, a throttle) hands the use back for free.
//
// Every failure collapses to ErrInvalid on purpose: an unknown code, a
// revoked one, an expired one, one already at its limit and one whose owner
// has been disabled all read the same to whoever typed it. Telling those
// apart would tell an attacker which guesses are close.
func (s *Store) Consume(ctx context.Context, q database.Queryer, rawCode string, now int64) (*Grant, error) {
	code := Normalise(rawCode)
	if code == "" {
		return nil, ErrInvalid
	}

	var (
		codeID, ownerID, groupID string
		groupDays, groupDaysMax  int
	)
	err := q.QueryRow(ctx,
		`SELECT id, owner_id, group_id, group_days, group_days_max FROM invite_codes WHERE code = ?`, code).
		Scan(&codeID, &ownerID, &groupID, &groupDays, &groupDaysMax)
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrInvalid
		}
		return nil, fmt.Errorf("invite: read code: %w", err)
	}

	// A personal code's owner can be disabled or removed after being
	// handed out. One code for all in the error either way, for the same
	// reason every other rejection here is: this is checked before the
	// spend below rather than after, so a disabled owner's code never
	// takes a use it is about to refuse anyway.
	if ownerID != "" {
		// Personal codes stop working when the operator switches personal
		// invites off, not only stop being handed out.
		userEnabled, _, _, _, _ := s.Settings()
		if !userEnabled {
			return nil, ErrInvalid
		}
		// The owner's row is held until the registration commits: the
		// owner's status and their count of invites are read here and
		// relied on by the spend below, and an operator disabling the owner
		// or a second registration through the same code must wait.
		locked, err := q.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, ownerID)
		if err != nil {
			return nil, fmt.Errorf("invite: lock owner: %w", err)
		}
		if present, err := locked.RowsAffected(); err != nil {
			return nil, fmt.Errorf("invite: lock owner: %w", err)
		} else if present != 1 {
			return nil, ErrInvalid
		}
		var ownerStatus string
		if err := q.QueryRow(ctx, `SELECT status FROM users WHERE id = ?`, ownerID).Scan(&ownerStatus); err != nil {
			return nil, fmt.Errorf("invite: read owner: %w", err)
		}
		if ownerStatus != string(user.StatusActive) {
			return nil, ErrInvalid
		}
		// The per-account limit is on rewards (see evaluateReward), not on
		// registrations through the personal code: a personal code carries an
		// unlimited max_uses, so someone registering through it still joins
		// successfully; only the reward to the inviter stops crediting.
	}

	// The one atomic step: every condition Consume promises is re-checked
	// here, in the WHERE clause, so nothing between the SELECT above and
	// this UPDATE can be raced. Two registrations spending the last use of
	// a max_uses=1 code concurrently both read the row above; only one's
	// UPDATE matches a row.
	result, err := q.Exec(ctx,
		`UPDATE invite_codes SET uses = uses + 1
		 WHERE id = ? AND revoked_at = 0
		   AND (max_uses = 0 OR uses < max_uses)
		   AND (expires_at = 0 OR expires_at > ?)`,
		codeID, now)
	if err != nil {
		return nil, fmt.Errorf("invite: consume: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("invite: consume: %w", err)
	}
	if affected != 1 {
		return nil, ErrInvalid
	}

	if groupID != "" {
		// A group named on the code can be deleted after being issued. The
		// same rule auth.Service.registrationGroup applies to the instance
		// default: a dangling reference is dropped rather than failing a
		// registration that has already spent its use.
		var exists int
		switch err := q.QueryRow(ctx, `SELECT 1 FROM user_groups WHERE id = ?`, groupID).Scan(&exists); {
		case database.IsNotFound(err):
			groupID, groupDays, groupDaysMax = "", 0, 0
		case err != nil:
			return nil, fmt.Errorf("invite: read group: %w", err)
		}
	}

	days := int64(groupDays)
	if groupID != "" {
		days = randomGroupDays(groupDays, groupDaysMax)
	}

	return &Grant{CodeID: codeID, Code: code, OwnerID: ownerID, GroupID: groupID, GroupDays: days}, nil
}

// randomGroupDays draws the trial length a registration or a claim actually
// grants: a fixed number when groupDaysMax does not exceed it, otherwise a
// uniform random whole number of days in [groupDays, groupDaysMax] — the
// partner-trial shape, where the exact length is not the point but a
// plausible spread of them is. Shared by Consume and Claim so the two draws
// cannot drift into disagreeing about what "days" means.
func randomGroupDays(groupDays, groupDaysMax int) int64 {
	if groupDaysMax > groupDays {
		// math/rand/v2, not crypto/rand: a trial length is not a secret,
		// and this only has to spread registrations across a range, not
		// resist being guessed.
		return int64(groupDays + mathrand.IntN(groupDaysMax-groupDays+1))
	}
	return int64(groupDays)
}

// RecordUse writes the row Consume's spend is remembered by, inside the same
// transaction. Kept separate from Consume rather than folded into it because
// the caller (Register or Provision) decides the user id only after Consume
// has already resolved the group — the account does not exist yet when
// Consume runs.
func (s *Store) RecordUse(ctx context.Context, q database.Queryer, codeID, userID, inviterID string, groupDays int64) error {
	_, err := q.Exec(ctx,
		`INSERT INTO invite_uses (user_id, code_id, inviter_id, group_days, created_at, rewarded_at, reward_cards, reward_skipped)
		 VALUES (?, ?, ?, ?, ?, 0, 0, '')`,
		userID, codeID, inviterID, groupDays, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("invite: record use: %w", err)
	}
	return nil
}

// Claim's own rate limit — see ClaimThrottled and the invite_claim_throttle
// migration comment for why this is a database row rather than
// auth.Limiter's in-memory bucket.
const (
	claimFailureWindow = time.Hour
	maxClaimFailures   = 10
)

// ClaimThrottled is Claim's rate-limit refusal, the same shape as
// auth.RateLimitError so the HTTP layer can send the same Retry-After header
// without this package importing auth.
type ClaimThrottled struct{ RetryAfter time.Duration }

func (e *ClaimThrottled) Error() string {
	return fmt.Sprintf("invite: too many attempts; try again in %s", e.RetryAfter.Round(time.Second))
}

// Rate limit on regenerating personal invite codes: at most 10 in an hour.
const (
	regenerateWindow = time.Hour
	maxRegenerates   = 10
)

// RegenerateThrottled is Regenerate's rate-limit refusal, mirroring ClaimThrottled.
type RegenerateThrottled struct{ RetryAfter time.Duration }

func (e *RegenerateThrottled) Error() string {
	return fmt.Sprintf("invite: too many attempts; try again in %s", e.RetryAfter.Round(time.Second))
}

// ClaimResult is what Claim hands back on success: the group the account now
// carries and the expiry that landed on it.
type ClaimResult struct {
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
	Days      int64  `json:"days"`
	ExpiresAt int64  `json:"expires_at"`
}

// claimOutcome is the plain-value result of one Claim attempt, decided inside
// its transaction and translated to a Go error only after that transaction
// has committed — the same technique evaluateReward uses for "reason", and
// for the same cause: the throttle bookkeeping below has to survive exactly
// the attempts that did not otherwise succeed, so the transaction that
// records a failure must be the one that commits, not one Claim rolls back
// because the claim itself was refused.
type claimOutcome string

const (
	claimOutcomeSuccess       claimOutcome = "success"
	claimOutcomeInvalid       claimOutcome = "invalid"
	claimOutcomeClaimed       claimOutcome = "claimed"
	claimOutcomeGroupConflict claimOutcome = "group_conflict"
	claimOutcomeOwnCode       claimOutcome = "own_code"
	claimOutcomeThrottled     claimOutcome = "throttled"
)

// Claim spends a batch or partner code's allow_existing use for an account
// that already exists — POST /api/profile/invites/claim's whole
// implementation, all of it inside one transaction holding the account's own
// row lock, the same per-account invariant PersonalCode and Regenerate use.
//
// Every rejection that is not the rate limit collapses to ErrInvalid the
// same way Consume's does — an unknown code, a revoked one, an expired one,
// one already at its limit, a personal code, and one that never allowed
// existing accounts at all all read the same to whoever typed it. Already
// having claimed this code (ErrClaimed) and a group membership Claim refuses
// to touch (ErrGroupConflict) are told apart, because both are real states
// an already-identified account can be shown rather than a guess at a door
// with no session behind it.
func (s *Store) Claim(ctx context.Context, userID, rawCode string) (*ClaimResult, error) {
	code := Normalise(rawCode)
	now := time.Now()
	nowMS := now.UnixMilli()

	var (
		outcome    claimOutcome
		retryAfter time.Duration
		result     ClaimResult
	)

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Per-account invariant: lock the account's own row, the AGENTS.md
		// spelling — one lock serves both invariants below, the claim
		// throttle and the group-membership decision, the same way Reward
		// takes the inviter's row once and evaluates several things under
		// it.
		locked, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID)
		if err != nil {
			return fmt.Errorf("invite: lock account: %w", err)
		}
		if present, err := locked.RowsAffected(); err != nil {
			return fmt.Errorf("invite: lock account: %w", err)
		} else if present != 1 {
			return fmt.Errorf("invite: claim: account %s not found", userID)
		}

		windowStart, failures, err := readClaimThrottle(ctx, tx, userID, nowMS)
		if err != nil {
			return err
		}
		if windowStart != 0 && failures >= maxClaimFailures {
			outcome = claimOutcomeThrottled
			retryAfter = time.Duration(windowStart+claimFailureWindow.Milliseconds()-nowMS) * time.Millisecond
			return nil
		}
		freshWindow := windowStart
		if freshWindow == 0 {
			freshWindow = nowMS
		}
		// fail is for a guess: an unknown, wrong-shaped or no-longer-valid
		// code, exactly what the throttle exists to slow down. refuse is for
		// a real, deterministic state a legitimate account can reach with a
		// perfectly good code — already claimed it, or a group membership
		// Claim will not touch — and must not cost against the same budget:
		// a double-submitted form or a second tab hitting either one over
		// and over is not a guessing attack.
		fail := func(which claimOutcome) error {
			outcome = which
			return writeClaimThrottle(ctx, tx, userID, freshWindow, failures+1)
		}
		refuse := func(which claimOutcome) error {
			outcome = which
			return nil
		}

		if code == "" {
			return fail(claimOutcomeInvalid)
		}

		var record struct {
			id                      string
			kind                    string
			createdBy               string
			allowExisting           bool
			groupID                 string
			groupDays, groupDaysMax int
			maxUses, uses           int
			expiresAt, revokedAt    int64
		}
		err = tx.QueryRow(ctx,
			`SELECT id, kind, created_by, allow_existing, group_id, group_days, group_days_max, max_uses, uses, expires_at, revoked_at
			 FROM invite_codes WHERE code = ?`, code).
			Scan(&record.id, &record.kind, &record.createdBy, &record.allowExisting, &record.groupID,
				&record.groupDays, &record.groupDaysMax, &record.maxUses, &record.uses,
				&record.expiresAt, &record.revokedAt)
		if database.IsNotFound(err) {
			return fail(claimOutcomeInvalid)
		}
		if err != nil {
			return fmt.Errorf("invite: read code: %w", err)
		}

		switch {
		case record.kind != CodeKindBatch && record.kind != CodeKindPartner:
			// Covers a personal code by name, and is the same refusal any
			// future kind this package does not yet know about gets.
			return fail(claimOutcomeInvalid)
		case !record.allowExisting:
			return fail(claimOutcomeInvalid)
		case record.revokedAt != 0:
			return fail(claimOutcomeInvalid)
		case record.expiresAt != 0 && record.expiresAt <= nowMS:
			return fail(claimOutcomeInvalid)
		case record.maxUses != 0 && record.uses >= record.maxUses:
			return fail(claimOutcomeInvalid)
		}

		// After the code's own validity, so a code that could never be
		// claimed still gets the one answer every other claimant gets.
		if record.createdBy != "" && record.createdBy == userID {
			return refuse(claimOutcomeOwnCode)
		}

		var alreadyClaimed int
		err = tx.QueryRow(ctx, `SELECT 1 FROM invite_claims WHERE code_id = ? AND user_id = ?`,
			record.id, userID).Scan(&alreadyClaimed)
		if err == nil {
			return refuse(claimOutcomeClaimed)
		}
		if !database.IsNotFound(err) {
			return fmt.Errorf("invite: read claim: %w", err)
		}

		// "One claim per account per code" is a promise about the code, not
		// about the invite_claims table specifically — an account that
		// registered through this exact code already spent its one grant
		// from it, the same as one that already claimed it, so Claim must
		// refuse here too rather than letting invite_uses and invite_claims
		// each think the account has never touched this code.
		var alreadyRegistered int
		err = tx.QueryRow(ctx, `SELECT 1 FROM invite_uses WHERE code_id = ? AND user_id = ?`,
			record.id, userID).Scan(&alreadyRegistered)
		if err == nil {
			return refuse(claimOutcomeClaimed)
		}
		if !database.IsNotFound(err) {
			return fmt.Errorf("invite: read use: %w", err)
		}

		if record.groupID != "" {
			// A group named on the code can be deleted after being issued —
			// invite_codes.group_id carries no foreign key of its own, the
			// same reason Consume re-checks this. Dropped rather than
			// refused: the code still spends, it just grants no group,
			// since the same rule applies to a registration through it.
			var exists int
			switch err := tx.QueryRow(ctx, `SELECT 1 FROM user_groups WHERE id = ?`, record.groupID).Scan(&exists); {
			case database.IsNotFound(err):
				record.groupID, record.groupDays, record.groupDaysMax = "", 0, 0
			case err != nil:
				return fmt.Errorf("invite: read group: %w", err)
			}
		}

		account, err := s.users.ByID(ctx, tx, userID)
		if err != nil {
			return fmt.Errorf("invite: read account: %w", err)
		}
		// A membership that lapsed between the account's last request and
		// this one is retired before Claim reads it, the same way a session
		// resolves it at sign-in — otherwise a stale, already-expired
		// GroupID could read as "some other group" and refuse a claim that
		// should have started fresh from the default one.
		account, err = s.users.ResolveMembership(ctx, tx, account)
		if err != nil {
			return fmt.Errorf("invite: resolve membership: %w", err)
		}

		defaultGroupID, err := s.registrationGroupID(ctx, tx)
		if err != nil {
			return err
		}

		days := randomGroupDays(record.groupDays, record.groupDaysMax)
		applyGroup := record.groupID != ""
		var newGroupID string
		var newExpiresAt int64
		if applyGroup {
			switch {
			case account.GroupID == defaultGroupID || account.GroupID == "":
				// Starting fresh from the group everybody without a code
				// lands in: the code's group, for exactly what it grants.
				// days <= 0 denotes permanent membership, matching the
				// registration path (auth.Service.applyInvite) where
				// group_expires_at is left 0.
				newGroupID = record.groupID
				if days > 0 {
					newExpiresAt = nowMS + days*86400000
				}
			case account.GroupID == record.groupID && account.GroupExpiresAt != 0:
				// Already sitting in this very group on a trial: topped up,
				// or upgraded to permanent membership if days <= 0.
				newGroupID = record.groupID
				if days > 0 {
					base := account.GroupExpiresAt
					if nowMS > base {
						base = nowMS
					}
					newExpiresAt = base + days*86400000
				}
			default:
				// Permanently in the code's own group, or a member of any
				// other one: never downgraded or replaced by a claim.
				return refuse(claimOutcomeGroupConflict)
			}
		}

		spent, err := tx.Exec(ctx,
			`UPDATE invite_codes SET uses = uses + 1
			 WHERE id = ? AND revoked_at = 0
			   AND (max_uses = 0 OR uses < max_uses)
			   AND (expires_at = 0 OR expires_at > ?)`,
			record.id, nowMS)
		if err != nil {
			return fmt.Errorf("invite: spend claim: %w", err)
		}
		if affected, err := spent.RowsAffected(); err != nil {
			return fmt.Errorf("invite: spend claim: %w", err)
		} else if affected != 1 {
			// Raced away between the read above and here — became revoked,
			// expired or exhausted by a concurrent claim or registration.
			return fail(claimOutcomeInvalid)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO invite_claims (code_id, user_id, group_days, created_at) VALUES (?, ?, ?, ?)`,
			record.id, userID, days, nowMS); err != nil {
			if isUnique(err) {
				// Defence in depth: the account's own row lock above already
				// serialises every concurrent Claim for this account, so two
				// transactions racing to insert the same (code_id, user_id)
				// should never both reach here. If one somehow does, the use
				// just spent is handed back rather than left paid for a
				// claim this call is about to refuse.
				if _, err := tx.Exec(ctx, `UPDATE invite_codes SET uses = uses - 1 WHERE id = ?`, record.id); err != nil {
					return fmt.Errorf("invite: refund claim: %w", err)
				}
				return refuse(claimOutcomeClaimed)
			}
			return fmt.Errorf("invite: record claim: %w", err)
		}

		if applyGroup {
			if _, err := s.users.UpdateAdminFields(ctx, tx, userID, user.AdminUpdate{
				GroupID: &newGroupID, GroupExpiresAt: &newExpiresAt,
			}); err != nil {
				return fmt.Errorf("invite: apply claim group: %w", err)
			}
		}

		groupName := ""
		if applyGroup {
			if g, err := s.groups.ByID(ctx, tx, record.groupID); err == nil {
				groupName = g.Name
			}
		}
		result = ClaimResult{GroupID: record.groupID, GroupName: groupName, Days: days, ExpiresAt: newExpiresAt}
		outcome = claimOutcomeSuccess
		return writeClaimThrottle(ctx, tx, userID, 0, 0)
	})
	if err != nil {
		return nil, err
	}

	switch outcome {
	case claimOutcomeThrottled:
		return nil, &ClaimThrottled{RetryAfter: retryAfter}
	case claimOutcomeInvalid:
		return nil, ErrInvalid
	case claimOutcomeClaimed:
		return nil, ErrClaimed
	case claimOutcomeGroupConflict:
		return nil, ErrGroupConflict
	case claimOutcomeOwnCode:
		return nil, ErrOwnCode
	default:
		return &result, nil
	}
}

// registrationGroupID mirrors auth.Service's own unexported registrationGroup:
// the configured default when it still exists, the instance's own default
// group otherwise. Claim needs the same notion of "the group somebody
// joining without a code lands in" to decide whether an account claiming a
// code is starting fresh or already sitting in some other membership — the
// two packages cannot share the method itself (see auth.InviteGrant's
// comment on why auth and invite do not import each other), so this is
// deliberately the same three lines rather than a shared helper neither side
// would otherwise depend on.
func (s *Store) registrationGroupID(ctx context.Context, q database.Queryer) (string, error) {
	if configured := s.settings.Get(settings.RegistrationGroup); configured != "" {
		if _, err := s.groups.ByID(ctx, q, configured); err == nil {
			return configured, nil
		}
	}
	fallback, err := s.groups.Default(ctx, q)
	if err != nil {
		if errors.Is(err, group.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return fallback.ID, nil
}

// readClaimThrottle reads the account's current claim-failure window, rolling
// it over to a fresh one — without writing anything — when the hour has
// already elapsed. windowStart == 0 back from here always means "no active
// block", whether that is because the account has never failed a claim or
// because its last block expired.
func readClaimThrottle(ctx context.Context, q database.Queryer, userID string, nowMS int64) (windowStart int64, failures int, err error) {
	err = q.QueryRow(ctx, `SELECT window_start, failures FROM invite_claim_throttle WHERE user_id = ?`, userID).
		Scan(&windowStart, &failures)
	if database.IsNotFound(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("invite: read claim throttle: %w", err)
	}
	if nowMS-windowStart >= claimFailureWindow.Milliseconds() {
		return 0, 0, nil
	}
	return windowStart, failures, nil
}

// writeClaimThrottle stores the account's claim-failure window: (0, 0) clears
// it on a successful claim, and any other pair records a failure under the
// window Claim already decided on — fresh or continuing — while it held the
// account's row lock.
func writeClaimThrottle(ctx context.Context, q database.Queryer, userID string, windowStart int64, failures int) error {
	_, err := q.Exec(ctx,
		`INSERT INTO invite_claim_throttle (user_id, window_start, failures) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET window_start = excluded.window_start, failures = excluded.failures`,
		userID, windowStart, failures)
	if err != nil {
		return fmt.Errorf("invite: write claim throttle: %w", err)
	}
	return nil
}

// readRegenerateThrottle reads the account's current regenerate window, rolling
// it over when the hour has elapsed.
func readRegenerateThrottle(ctx context.Context, q database.Queryer, userID string, nowMS int64) (windowStart int64, count int, err error) {
	err = q.QueryRow(ctx, `SELECT window_start, count FROM invite_regenerate_throttle WHERE user_id = ?`, userID).
		Scan(&windowStart, &count)
	if database.IsNotFound(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("invite: read regenerate throttle: %w", err)
	}
	if nowMS-windowStart >= regenerateWindow.Milliseconds() {
		return 0, 0, nil
	}
	return windowStart, count, nil
}

// writeRegenerateThrottle stores the account's regenerate window.
func writeRegenerateThrottle(ctx context.Context, q database.Queryer, userID string, windowStart int64, count int) error {
	_, err := q.Exec(ctx,
		`INSERT INTO invite_regenerate_throttle (user_id, window_start, count) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET window_start = excluded.window_start, count = excluded.count`,
		userID, windowStart, count)
	if err != nil {
		return fmt.Errorf("invite: write regenerate throttle: %w", err)
	}
	return nil
}

// Reward resolves the personal-code invite reward for userID: whether the
// invitee now qualifies to trigger it, and if so whether the inviter is
// actually paid or the reward is skipped and why.
//
// Called right after registration, again when the account verifies its
// email, and by RewardPending on the janitor's pass — "qualifies" can change
// between them: an account created unverified qualifies for nothing on an
// instance that requires verification, and one the signup review restricted
// qualifies once the restriction lifts, which nothing tells this package
// about. verificationRequired is passed in rather than
// read from a setting here because whether verification is actually in
// force also depends on whether mail is configured at all — knowledge that
// belongs to auth.Service.VerificationRequired, not duplicated here.
func (s *Store) Reward(ctx context.Context, userID string, verificationRequired bool) error {
	var (
		codeID, inviterID string
		rewardedAt        int64
	)
	err := s.db.QueryRow(ctx,
		`SELECT code_id, inviter_id, rewarded_at FROM invite_uses WHERE user_id = ?`, userID).
		Scan(&codeID, &inviterID, &rewardedAt)
	if err != nil {
		if database.IsNotFound(err) {
			// Never registered through a code at all.
			return nil
		}
		return fmt.Errorf("invite: read use: %w", err)
	}
	// Admin-issued codes have nobody to reward, and a use already resolved
	// (granted or skipped) is not resolved twice.
	if inviterID == "" || rewardedAt != 0 {
		return nil
	}

	invitee, err := s.users.ByID(ctx, nil, userID)
	if err != nil {
		return fmt.Errorf("invite: read invitee: %w", err)
	}
	now := time.Now()
	qualifies := invitee.IsActive() && !invitee.APIRestrictedAt(now) &&
		(!verificationRequired || invitee.EmailVerified)
	if !qualifies {
		// Left unclaimed on purpose: the next call — at verification, if
		// that is what this account is still waiting on — asks again.
		return nil
	}

	// One transaction, holding the inviter's row, for the whole decision.
	// The limit is read and then written, so two invitees qualifying at
	// once must not both find the inviter under it; and the claim commits
	// only together with the cards it pays, so a grant that fails leaves
	// the use unresolved for the next attempt rather than marked paid.
	var (
		reason  string
		cards   int
		counted int
		every   int
		claimed bool
	)
	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, inviterID)
		if err != nil {
			return fmt.Errorf("invite: lock inviter: %w", err)
		}
		present, err := locked.RowsAffected()
		if err != nil {
			return fmt.Errorf("invite: lock inviter: %w", err)
		}
		var days int
		reason, cards, days, counted, every, err = s.evaluateReward(ctx, tx, invitee, inviterID, present == 1)
		if err != nil {
			return err
		}

		// The claim: a second caller for the same invitee — registration
		// and an eager click on the verification link, say — affects no
		// row once this one has committed, and returns having done nothing.
		result, err := tx.Exec(ctx,
			`UPDATE invite_uses SET rewarded_at = ?, reward_cards = ?, reward_skipped = ?
			 WHERE user_id = ? AND rewarded_at = 0`,
			now.UnixMilli(), cards, reason, userID)
		if err != nil {
			return fmt.Errorf("invite: claim reward: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("invite: claim reward: %w", err)
		}
		if affected != 1 {
			return nil
		}
		claimed = true
		if reason != "" {
			return nil
		}
		// Persist the new running total on the inviter's own row, still
		// under the lock taken above: countedInvites reads this column
		// back, and it must only ever grow, never be recomputed from
		// invite_uses rows a later, unrelated account deletion can remove.
		if _, err := tx.Exec(ctx, `UPDATE users SET invite_reward_count = ? WHERE id = ?`, counted, inviterID); err != nil {
			return fmt.Errorf("invite: persist reward count: %w", err)
		}
		// cards is 0 on a counted invite that did not land on the every-N
		// milestone: still claimed, still notified below, just nothing to
		// grant this time.
		if cards > 0 {
			if _, err := s.cards.Grant(ctx, tx, inviterID, cards, days); err != nil {
				return fmt.Errorf("invite: grant reward cards: %w", err)
			}
		}
		return nil
	})
	if err != nil || !claimed || reason != "" {
		return err
	}

	if s.Notify != nil {
		_, _, instanceRewardCards, _, _ := s.Settings()
		notifyCtx := context.WithoutCancel(ctx)
		if err := s.Notify.Push(notifyCtx, nil, notify.Notification{
			Audience: notify.AudienceUser, UserID: inviterID,
			Kind: "invite_joined",
			Params: map[string]any{
				"username":  invitee.Username,
				"counted":   counted,
				"every":     every,
				"cards":     cards,
				"remaining": nextRewardIn(counted, every, instanceRewardCards),
			},
			Link: "/settings?tab=invites",
		}); err != nil {
			return fmt.Errorf("invite: notify inviter: %w", err)
		}
	}
	return nil
}

// RewardPending asks again for every recent use still waiting on a reward.
// Registration and verification are the two moments Reward is otherwise
// called, and an invitee can come to qualify at neither: a restriction the
// AI review placed at sign-up that later runs out, or that an operator
// lifts. Bounded to the last month and a page at a time, so a backlog is
// worked through over a few passes instead of all at once.
func (s *Store) RewardPending(ctx context.Context, verificationRequired bool) error {
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM invite_uses
		 WHERE rewarded_at = 0 AND inviter_id <> '' AND created_at > ?
		 ORDER BY created_at LIMIT 200`,
		time.Now().Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		return fmt.Errorf("invite: read pending rewards: %w", err)
	}
	var pending []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return fmt.Errorf("invite: read pending rewards: %w", err)
		}
		pending = append(pending, userID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("invite: read pending rewards: %w", err)
	}
	var lastErr error
	for _, userID := range pending {
		if err := s.Reward(ctx, userID, verificationRequired); err != nil {
			slog.ErrorContext(ctx, "invite: resolve pending reward failed", "user_id", userID, "error", err)
			lastErr = err
		}
	}
	return lastErr
}

// evaluateReward decides whether the inviter is actually paid, and how much,
// once the invitee is known to qualify at all. q is Reward's transaction,
// which already holds the inviter's row: every read here goes through it,
// since on SQLite a second connection would wait behind that very lock.
//
// counted is the inviter's total of qualifying invites once this one is
// included — every invite that reaches reason == "" is one, whether or not
// it happened to land on this cadence's milestone — and cards is what this
// particular invite pays: rewardCards exactly when counted is a multiple of
// every, zero otherwise. A reason for skipping never counts at all, the same
// as before this feature: same_ip, limit, disabled and a gone or disabled
// inviter all leave the running total untouched.
func (s *Store) evaluateReward(
	ctx context.Context, q database.Queryer, invitee user.User, inviterID string, present bool,
) (reason string, cards, days, counted, every int, err error) {
	userEnabled, limit, rewardCards, rewardCardDays, rewardEvery := s.Settings()
	every = rewardEvery

	// An inviter deleted since the invite has nobody to pay, and one
	// disabled since has been judged by an operator; either way the use is
	// resolved rather than left for the cards to fail against forever.
	if !present {
		return "inviter_gone", 0, 0, 0, every, nil
	}
	inviter, err := s.users.ByID(ctx, q, inviterID)
	if err != nil {
		return "", 0, 0, 0, every, fmt.Errorf("invite: read inviter: %w", err)
	}
	if !inviter.IsActive() {
		return "inviter_disabled", 0, 0, 0, every, nil
	}
	// Switching personal invites off stops the payouts too, not only new
	// codes: the operator turning it off is usually doing so because of how
	// it was being used.
	if !userEnabled || rewardCards <= 0 {
		return "disabled", 0, 0, 0, every, nil
	}
	if sameIP(ctx, q, invitee, inviter) {
		return "same_ip", 0, 0, 0, every, nil
	}
	already, err := countedInvites(ctx, q, inviterID)
	if err != nil {
		return "", 0, 0, 0, every, err
	}
	if limit > 0 && already >= limit {
		return "limit", 0, 0, already, every, nil
	}

	counted = already + 1
	if counted%every == 0 {
		cards = rewardCards
	}
	return "", cards, rewardCardDays, counted, every, nil
}

// sameIP catches an inviter rewarding themselves: a second account signed up
// from the address the first registered from, or from an address the first
// is still signed in on.
func sameIP(ctx context.Context, q database.Queryer, invitee, inviter user.User) bool {
	if invitee.SignupIP == "" {
		return false
	}
	if inviter.SignupIP != "" && inviter.SignupIP == invitee.SignupIP {
		return true
	}
	var exists int
	// Best effort: a read failure here must not be able to leave a reward
	// stuck forever, so it reads as "no match" rather than failing Reward.
	err := q.QueryRow(ctx,
		`SELECT 1 FROM sessions WHERE user_id = ? AND ip = ? AND expires_at > ? LIMIT 1`,
		inviter.ID, invitee.SignupIP, time.Now().UnixMilli()).Scan(&exists)
	return err == nil
}

// countedInvites is the inviter's running total of qualifying invites —
// resolved, not skipped — the figure invites.user_limit caps and
// invites.reward_every divides into milestones. Also what GET
// /api/profile/invites shows an account as its own "counted".
//
// Read from users.invite_reward_count, a durable counter, rather than
// `SELECT COUNT(*) FROM invite_uses WHERE ...`: invite_uses.user_id cascades
// away when that invitee's account is later deleted, and a live COUNT(*)
// would then drop, letting a milestone already paid be paid again once new
// invitees bring the (recomputed) count back up to it. See evaluateReward,
// which is the only writer of this column.
func countedInvites(ctx context.Context, q database.Queryer, inviterID string) (int, error) {
	var count int
	err := q.QueryRow(ctx, `SELECT invite_reward_count FROM users WHERE id = ?`, inviterID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("invite: count rewards: %w", err)
	}
	return count, nil
}

// PersonalCode returns account's own invite code, creating one under its row
// lock if it does not already have a live one — so two tabs opening the
// invites panel at once cannot mint two.
func (s *Store) PersonalCode(ctx context.Context, userID string) (Code, error) {
	var record Code
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Per-account invariant: lock the owner's row, the AGENTS.md
		// spelling.
		if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("invite: lock account: %w", err)
		}
		existing, err := s.ownedCode(ctx, tx, userID)
		if err == nil {
			record = existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		record, err = s.createPersonal(ctx, tx, userID)
		return err
	})
	if err != nil {
		return Code{}, err
	}
	return record, nil
}

// Regenerate revokes the current personal code and issues a new one, under
// the same row lock PersonalCode uses. Limited to 10 regenerations per hour.
func (s *Store) Regenerate(ctx context.Context, userID string) (Code, error) {
	var record Code
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("invite: lock account: %w", err)
		}
		nowMS := time.Now().UnixMilli()
		windowStart, count, err := readRegenerateThrottle(ctx, tx, userID, nowMS)
		if err != nil {
			return err
		}
		if count >= maxRegenerates {
			remaining := regenerateWindow - time.Duration(nowMS-windowStart)*time.Millisecond
			if remaining <= 0 {
				remaining = time.Second
			}
			return &RegenerateThrottled{RetryAfter: remaining}
		}
		freshWindow := windowStart
		if freshWindow == 0 {
			freshWindow = nowMS
		}
		if err := writeRegenerateThrottle(ctx, tx, userID, freshWindow, count+1); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE invite_codes SET revoked_at = ? WHERE owner_id = ? AND revoked_at = 0`,
			nowMS, userID); err != nil {
			return fmt.Errorf("invite: revoke personal code: %w", err)
		}
		record, err = s.createPersonal(ctx, tx, userID)
		return err
	})
	if err != nil {
		return Code{}, err
	}
	return record, nil
}

func (s *Store) ownedCode(ctx context.Context, q database.Queryer, userID string) (Code, error) {
	now := time.Now().UnixMilli()
	var record Code
	err := q.QueryRow(ctx,
		`SELECT id, code, max_uses, uses, expires_at, revoked_at, created_at
		 FROM invite_codes WHERE owner_id = ? AND revoked_at = 0
		 ORDER BY created_at DESC LIMIT 1`, userID).
		Scan(&record.ID, &record.Code, &record.MaxUses, &record.Uses,
			&record.ExpiresAt, &record.RevokedAt, &record.CreatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Code{}, ErrNotFound
		}
		return Code{}, fmt.Errorf("invite: read personal code: %w", err)
	}
	record.OwnerID = userID
	record.Kind = CodeKindPersonal
	record.Status = status(record.RevokedAt, record.ExpiresAt, record.MaxUses, record.Uses, now)
	return record, nil
}

// createPersonal mints a fresh, unlimited-use code for an account. A retry
// budget rather than one attempt: a collision on the generated text is
// vanishingly rare with thirty symbols and eight characters, but costs
// nothing to retry and should never reach a caller as a failure.
func (s *Store) createPersonal(ctx context.Context, q database.Queryer, userID string) (Code, error) {
	now := time.Now().UnixMilli()
	for attempt := 0; attempt < 3; attempt++ {
		generated, err := generateCode()
		if err != nil {
			return Code{}, err
		}
		record := Code{ID: id.New(), Code: generated, OwnerID: userID, Kind: CodeKindPersonal, CreatedAt: now, Status: StatusActive}
		res, err := q.Exec(ctx, `INSERT INTO invite_codes
			(id, code, owner_id, kind, name, allow_existing, group_id, group_days, group_days_max, max_uses, uses, expires_at, revoked_at, note, created_by, created_at)
			VALUES (?, ?, ?, ?, '', ?, '', 0, 0, 0, 0, 0, 0, '', ?, ?)
			ON CONFLICT (code) DO NOTHING`,
			record.ID, record.Code, record.OwnerID, record.Kind, false, userID, record.CreatedAt)
		if err != nil {
			return Code{}, fmt.Errorf("invite: create personal code: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return Code{}, fmt.Errorf("invite: create personal code: %w", err)
		}
		if affected == 1 {
			return record, nil
		}
	}
	return Code{}, fmt.Errorf("invite: create personal code: collision retry exhausted")
}

func isUnique(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate") ||
		strings.Contains(message, "constraint")
}

// Uses lists every registration and every claim through one code, newest
// first — an account's own list when the code is its personal one, or an
// administrator's view of one it issued. The two sources are unioned rather
// than read separately and merged in Go: invite_uses seats a brand new
// account and may earn its inviter a reward, invite_claims adds time to one
// that already exists and rewards nobody, and Via is how a reader (and this
// method's own caller) tells which row came from which.
// maxUsesListed caps the rows Uses returns, protecting both the profile
// invite panel and the administrator's code inspection from unbounded lists.
const maxUsesListed = 100

func (s *Store) Uses(ctx context.Context, codeID string) ([]Use, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, username, nickname, group_days, created_at,
		       rewarded_at, reward_cards, reward_skipped, via
		FROM (
			SELECT iu.user_id AS user_id, COALESCE(u.username, '') AS username, COALESCE(u.nickname, '') AS nickname,
			       iu.group_days AS group_days, iu.created_at AS created_at,
			       iu.rewarded_at AS rewarded_at, iu.reward_cards AS reward_cards, iu.reward_skipped AS reward_skipped,
			       ? AS via
			FROM invite_uses iu
			JOIN users u ON u.id = iu.user_id
			WHERE iu.code_id = ?
			UNION ALL
			SELECT ic.user_id AS user_id, COALESCE(u.username, '') AS username, COALESCE(u.nickname, '') AS nickname,
			       ic.group_days AS group_days, ic.created_at AS created_at,
			       0 AS rewarded_at, 0 AS reward_cards, '' AS reward_skipped,
			       ? AS via
			FROM invite_claims ic
			JOIN users u ON u.id = ic.user_id
			WHERE ic.code_id = ?
		) everything
		ORDER BY created_at DESC
		LIMIT ?`, ViaRegister, codeID, ViaClaim, codeID, maxUsesListed)
	if err != nil {
		return nil, fmt.Errorf("invite: list uses: %w", err)
	}
	defer rows.Close()

	out := []Use{}
	for rows.Next() {
		var record Use
		if err := rows.Scan(&record.UserID, &record.Username, &record.Nickname,
			&record.GroupDays, &record.CreatedAt, &record.RewardedAt,
			&record.RewardCards, &record.RewardSkipped, &record.Via); err != nil {
			return nil, fmt.Errorf("invite: scan use: %w", err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invite: list uses: %w", err)
	}
	return out, nil
}

// UsesByInviter returns up to maxUsesListed registrations made through any of
// this inviter's personal invite codes (including codes previously regenerated),
// ordered newest first.
func (s *Store) UsesByInviter(ctx context.Context, inviterID string) ([]Use, error) {
	rows, err := s.db.Query(ctx, `
		SELECT iu.user_id, COALESCE(u.username, ''), COALESCE(u.nickname, ''),
		       iu.group_days, iu.created_at, iu.rewarded_at, iu.reward_cards,
		       iu.reward_skipped, ? AS via
		FROM invite_uses iu
		JOIN users u ON u.id = iu.user_id
		WHERE iu.inviter_id = ?
		ORDER BY iu.created_at DESC
		LIMIT ?`, ViaRegister, inviterID, maxUsesListed)
	if err != nil {
		return nil, fmt.Errorf("invite: list uses by inviter: %w", err)
	}
	defer rows.Close()

	out := []Use{}
	for rows.Next() {
		var record Use
		if err := rows.Scan(&record.UserID, &record.Username, &record.Nickname,
			&record.GroupDays, &record.CreatedAt, &record.RewardedAt,
			&record.RewardCards, &record.RewardSkipped, &record.Via); err != nil {
			return nil, fmt.Errorf("invite: scan use: %w", err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invite: list uses by inviter: %w", err)
	}
	return out, nil
}
