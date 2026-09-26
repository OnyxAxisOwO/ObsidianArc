// Package card owns usage reset cards and the codes that mint them.
//
// A card is a one-shot permission to put an account's usage back to full. It
// deliberately knows nothing about how that reset is performed: the quota
// counters belong to internal/quota, and this package would have to import it
// to spend one. The handler takes a hook instead, wired in server.go, which
// is the same seam uploads and deletes already use.
package card

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

const (
	SourceGrant = "grant"
	SourceCode  = "code"

	MaxNameChars = 64
	MaxNoteChars = 200
	MaxCodeChars = 64
	// One code cannot mint an unbounded number of cards, and one grant cannot
	// hand out an unbounded number either. Both are a typo away from being an
	// accident nobody can undo one row at a time.
	MaxCards = 10000
	// One form submission cannot mint more codes than an operator can look
	// at afterwards, which is the only place they are ever shown in full.
	MaxBatch = 200
	MaxDays  = 3650
)

var (
	ErrNotFound      = errors.New("card: not found")
	ErrUsed          = errors.New("card: already used")
	ErrExpired       = errors.New("card: expired")
	ErrCodeUnknown   = errors.New("card: no such code")
	ErrCodeExpired   = errors.New("card: that code has expired")
	ErrCodeEmpty     = errors.New("card: that code has been fully redeemed")
	ErrCodeUsed      = errors.New("card: this account has already redeemed that code")
	ErrCodeTaken     = errors.New("card: that code already exists")
	ErrInvalidCode   = errors.New("card: a code is required")
	ErrInvalidCount  = errors.New("card: at least one card is required")
	ErrInvalidExpiry = errors.New("card: expiry must be in the future")
	ErrInvalidWindow = errors.New("card: invalid quota window")
	ErrNamedBatch    = errors.New("card: a batch is generated, so it cannot be given a code of its own")
)

// ValidWindows are the quota allowance windows that a card can target.
var ValidWindows = []string{"5h", "1w", "1m"}

func validateWindows(windows []string) error {
	for _, w := range windows {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || w == "full" {
			continue
		}
		if w != "5h" && w != "1w" && w != "1m" {
			return ErrInvalidWindow
		}
	}
	return nil
}

func normalizeWindows(windows []string) string {
	if len(windows) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	for _, w := range windows {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "full" {
			return ""
		}
		if w == "5h" || w == "1w" || w == "1m" {
			seen[w] = true
		}
	}
	var ordered []string
	for _, expected := range ValidWindows {
		if seen[expected] {
			ordered = append(ordered, expected)
		}
	}
	return strings.Join(ordered, ",")
}

func parseWindows(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "full" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Card is one reset, as its owner sees it.
type Card struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Windows   []string `json:"windows"`
	Source    string   `json:"source"`
	ExpiresAt int64    `json:"expires_at"`
	UsedAt    int64    `json:"used_at,omitempty"`
	CreatedAt int64    `json:"created_at"`
}

// Code is a batch of cards behind a string somebody types in.
type Code struct {
	ID        string   `json:"id"`
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	Windows   []string `json:"windows"`
	Cards     int      `json:"cards"`
	Claimed   int      `json:"claimed"`
	CardDays  int      `json:"card_days"`
	ExpiresAt int64    `json:"expires_at"`
	Note      string   `json:"note"`
	CreatedAt int64    `json:"created_at"`
}

// Redemption identifies who claimed one card from a code and when. It is an
// administrative view; the account-facing card API never exposes another
// user's identity.
type Redemption struct {
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	Nickname   string `json:"nickname"`
	RedeemedAt int64  `json:"redeemed_at"`
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

// --- cards ---------------------------------------------------------------------

// Available is what an account may still spend: unused, and not yet expired.
func (s *Store) Available(ctx context.Context, userID string) ([]Card, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, windows, source, expires_at, used_at, created_at FROM usage_cards
		 WHERE user_id = ? AND used_at = ? AND expires_at > ?
		 ORDER BY expires_at`,
		userID, 0, time.Now().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("card: list: %w", err)
	}
	defer rows.Close()

	out := []Card{}
	for rows.Next() {
		var record Card
		var rawWins string
		if err := rows.Scan(&record.ID, &record.Name, &rawWins, &record.Source, &record.ExpiresAt,
			&record.UsedAt, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("card: scan: %w", err)
		}
		record.Windows = parseWindows(rawWins)
		out = append(out, record)
	}
	return out, rows.Err()
}

// Holding is what an account has, at a glance.
//
// Counts and not just the list, because "none left" and "never had any" are
// different answers to "why can this person not reset", and the available
// list alone cannot tell them apart.
type Holding struct {
	Available int `json:"available"`
	Used      int `json:"used"`
	Expired   int `json:"expired"`
	Total     int `json:"total"`
	// The unused, unexpired ones, soonest to expire first — the ones an
	// operator might actually be asked about.
	Cards []Card `json:"cards"`
}

// Held summarises one account's cards.
func (s *Store) Held(ctx context.Context, userID string) (Holding, error) {
	now := time.Now().UnixMilli()

	var holding Holding
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN used_at = ? AND expires_at > ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN used_at <> ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN used_at = ? AND expires_at <= ? THEN 1 ELSE 0 END), 0),
			COUNT(*)
		FROM usage_cards WHERE user_id = ?`,
		0, now, 0, 0, now, userID).
		Scan(&holding.Available, &holding.Used, &holding.Expired, &holding.Total)
	if err != nil {
		return Holding{}, fmt.Errorf("card: held: %w", err)
	}

	cards, err := s.Available(ctx, userID)
	if err != nil {
		return Holding{}, err
	}
	holding.Cards = cards
	return holding, nil
}

// Spend marks one card used and reports whether it was this call that did it.
//
// The condition is in the UPDATE rather than in a read followed by a write:
// two tabs pressing the same button at once would otherwise both see an
// unused card and both reset the account, spending two cards for one reset.
func (s *Store) Spend(ctx context.Context, userID, cardID string) error {
	_, err := s.SpendCard(ctx, userID, cardID)
	return err
}

// SpendCard marks one card used and returns the spent Card so the caller knows
// which quota windows to reset.
func (s *Store) SpendCard(ctx context.Context, userID, cardID string) (Card, error) {
	now := time.Now().UnixMilli()
	result, err := s.db.Exec(ctx,
		`UPDATE usage_cards SET used_at = ?
		 WHERE id = ? AND user_id = ? AND used_at = ? AND expires_at > ?`,
		now, cardID, userID, 0, now)
	if err != nil {
		return Card{}, fmt.Errorf("card: spend: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 1 {
		var c Card
		var rawWins string
		err := s.db.QueryRow(ctx,
			`SELECT id, name, windows, source, expires_at, used_at, created_at
			 FROM usage_cards WHERE id = ? AND user_id = ?`,
			cardID, userID).Scan(&c.ID, &c.Name, &rawWins, &c.Source, &c.ExpiresAt, &c.UsedAt, &c.CreatedAt)
		if err != nil {
			return Card{}, fmt.Errorf("card: spend read: %w", err)
		}
		c.Windows = parseWindows(rawWins)
		return c, nil
	}

	// Nothing changed. Say which of the three reasons it was, because "that
	// card is gone" and "that card was never yours" want different answers.
	var used, expires int64
	err = s.db.QueryRow(ctx,
		`SELECT used_at, expires_at FROM usage_cards WHERE id = ? AND user_id = ?`,
		cardID, userID).Scan(&used, &expires)
	if err != nil {
		if database.IsNotFound(err) {
			return Card{}, ErrNotFound
		}
		return Card{}, fmt.Errorf("card: spend: %w", err)
	}
	if used != 0 {
		return Card{}, ErrUsed
	}
	return Card{}, ErrExpired
}

// SpendNextForWindow marks used the earliest expiring card that covers targetWindow.
// An empty targetWindow matches any card. A card with no windows (or "full") covers all windows.
func (s *Store) SpendNextForWindow(ctx context.Context, q database.Queryer, userID string, targetWindow string) (Card, error) {
	if q == nil {
		var spent Card
		err := s.db.Tx(ctx, func(tx *database.Tx) error {
			var err error
			spent, err = s.spendNextLocked(ctx, tx, userID, targetWindow)
			return err
		})
		return spent, err
	}
	return s.spendNextLocked(ctx, q, userID, targetWindow)
}

func (s *Store) spendNextLocked(ctx context.Context, q database.Queryer, userID string, targetWindow string) (Card, error) {
	now := time.Now().UnixMilli()
	targetWindow = strings.TrimSpace(strings.ToLower(targetWindow))

	query := `SELECT id, name, windows, source, expires_at, created_at
		FROM usage_cards
		WHERE user_id = ? AND used_at = ? AND expires_at > ?`
	args := []any{userID, 0, now}

	if targetWindow != "" && targetWindow != "full" {
		query += ` AND (windows = '' OR windows = 'full' OR windows LIKE ?)`
		args = append(args, "%"+targetWindow+"%")
	}

	query += ` ORDER BY expires_at, id LIMIT 1`

	var (
		c       Card
		rawWins string
	)
	err := q.QueryRow(ctx, query, args...).Scan(
		&c.ID, &c.Name, &rawWins, &c.Source, &c.ExpiresAt, &c.CreatedAt,
	)
	if err != nil {
		if database.IsNotFound(err) {
			return Card{}, ErrNotFound
		}
		return Card{}, fmt.Errorf("card: spend next query: %w", err)
	}

	result, err := q.Exec(ctx,
		`UPDATE usage_cards SET used_at = ?
		 WHERE id = ? AND user_id = ? AND used_at = ? AND expires_at > ?`,
		now, c.ID, userID, 0, now)
	if err != nil {
		return Card{}, fmt.Errorf("card: spend next update: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Card{}, ErrNotFound
	}

	c.UsedAt = now
	c.Windows = parseWindows(rawWins)
	return c, nil
}

// SpendNext marks the available card that expires first, regardless of window.
func (s *Store) SpendNext(ctx context.Context, q database.Queryer, userID string) error {
	_, err := s.SpendNextForWindow(ctx, q, userID, "")
	return err
}

// Grant hands cards to one account without a code in between. q is nil to
// grant on its own, or the caller's transaction when the grant has to stand
// or fall with something else — an invite reward's claim, which must not
// commit as paid when the cards it pays were never written.
func (s *Store) Grant(ctx context.Context, q database.Queryer, userID string, count, days int) ([]Card, error) {
	return s.GrantNamed(ctx, q, userID, count, days, "", nil)
}

// GrantNamed hands named cards with specified windows to an account.
func (s *Store) GrantNamed(ctx context.Context, q database.Queryer, userID string, count, days int, name string, windows []string) ([]Card, error) {
	if err := validateWindows(windows); err != nil {
		return nil, err
	}
	now := time.Now()
	return s.grant(ctx, q, userID, count, now.Add(time.Duration(clampDays(days))*24*time.Hour).UnixMilli(), now.UnixMilli(), name, windows)
}

// GrantUntil is the administrative spelling: the operator chose the expiry
// itself rather than a duration whose exact resulting date was implicit.
func (s *Store) GrantUntil(ctx context.Context, userID string, count int, expiresAt int64) ([]Card, error) {
	return s.GrantUntilNamed(ctx, userID, count, expiresAt, "", nil)
}

// GrantUntilNamed is the administrative spelling with custom name and quota windows.
func (s *Store) GrantUntilNamed(ctx context.Context, userID string, count int, expiresAt int64, name string, windows []string) ([]Card, error) {
	if err := validateWindows(windows); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	if expiresAt <= now || expiresAt > now+int64(MaxDays)*24*3600*1000 {
		return nil, ErrInvalidExpiry
	}
	return s.grant(ctx, nil, userID, count, expiresAt, now, name, windows)
}

// Reschedule moves the expiry of the cards one account is still holding.
//
// Unused cards only, but expired ones included, because the request that
// brings an operator here is "their card ran out, give them longer" — and a
// card that has already been spent is not a card any more, so moving its date
// would hand back a reset somebody already took.
//
// An empty cardIDs means every unused card that account holds. That is the
// bulk spelling, and it is the one an operator asks for: somebody with eleven
// cards wants all eleven moved, not eleven requests.
func (s *Store) Reschedule(ctx context.Context, userID string, cardIDs []string, expiresAt int64) (int, error) {
	now := time.Now().UnixMilli()
	if expiresAt <= now || expiresAt > now+int64(MaxDays)*24*3600*1000 {
		return 0, ErrInvalidExpiry
	}

	query := `UPDATE usage_cards SET expires_at = ? WHERE user_id = ? AND used_at = ?`
	args := []any{expiresAt, userID, 0}

	if len(cardIDs) > 0 {
		if len(cardIDs) > MaxCards {
			cardIDs = cardIDs[:MaxCards]
		}
		placeholders := make([]byte, 0, len(cardIDs)*2)
		for i, cardID := range cardIDs {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args = append(args, cardID)
		}
		query += ` AND id IN (` + string(placeholders) + `)`
	}

	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("card: reschedule: %w", err)
	}
	moved, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("card: reschedule: %w", err)
	}
	return int(moved), nil
}

func (s *Store) grant(ctx context.Context, q database.Queryer, userID string, count int, expires, now int64, name string, windows []string) ([]Card, error) {
	if count < 1 {
		return nil, ErrInvalidCount
	}
	count = min(count, MaxCards)
	cleanName := text.TrimAndTruncate(name, MaxNameChars)
	rawWindows := normalizeWindows(windows)
	parsedWin := parseWindows(rawWindows)

	out := make([]Card, 0, count)
	insert := func(tx database.Queryer) error {
		for range count {
			record := Card{
				ID: id.New(), Name: cleanName, Windows: parsedWin, Source: SourceGrant,
				ExpiresAt: expires, CreatedAt: now,
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO usage_cards (id, user_id, name, windows, source, code_id, expires_at, used_at, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				record.ID, userID, record.Name, rawWindows, record.Source, "", record.ExpiresAt, 0, record.CreatedAt); err != nil {
				return fmt.Errorf("card: grant: %w", err)
			}
			out = append(out, record)
		}
		return nil
	}
	var err error
	if q != nil {
		err = insert(q)
	} else {
		err = s.db.Tx(ctx, func(tx *database.Tx) error { return insert(tx) })
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Revoke takes one card back.
//
// Unused only, the same rule Reschedule follows: a spent card is the record
// of a reset that already happened, and deleting it would leave an account
// whose allowance was restored by nothing. The condition is in the DELETE so
// a card being spent in another tab at that moment survives rather than
// vanishing after it has already paid for a reset.
func (s *Store) Revoke(ctx context.Context, userID, cardID string) error {
	result, err := s.db.Exec(ctx,
		`DELETE FROM usage_cards WHERE id = ? AND user_id = ? AND used_at = ?`,
		cardID, userID, 0)
	if err != nil {
		return fmt.Errorf("card: revoke: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 1 {
		return nil
	}

	// Nothing went. Say which of the two reasons it was, because "that card
	// is not this account's" and "it has already been spent" are different
	// answers to somebody looking at a list that shows neither.
	var used int64
	err = s.db.QueryRow(ctx,
		`SELECT used_at FROM usage_cards WHERE id = ? AND user_id = ?`, cardID, userID).Scan(&used)
	if err != nil {
		if database.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("card: revoke: %w", err)
	}
	return ErrUsed
}

// --- codes ---------------------------------------------------------------------

type CodeInput struct {
	Code      string
	Name      string
	Windows   []string
	Cards     int
	CardDays  int
	ExpiresAt int64
	Note      string
}

// CreateCodes mints a batch.
//
// One code with a name somebody chose, or a stack of generated ones — the
// difference is only whether Code was filled in. A batch of ten is ten
// separate codes, each carrying its own cards, because "here is a code, it
// works ten times" and "here are ten codes" are different things to hand out
// and only the second can be given to ten people separately.
func (s *Store) CreateCodes(ctx context.Context, in CodeInput, count int) ([]Code, error) {
	if err := validateWindows(in.Windows); err != nil {
		return nil, err
	}
	if count < 1 {
		count = 1
	}
	count = min(count, MaxBatch)
	if count > 1 && strings.TrimSpace(in.Code) != "" {
		return nil, ErrNamedBatch
	}

	out := make([]Code, 0, count)
	for range count {
		attempt := in
		if strings.TrimSpace(attempt.Code) == "" {
			generated, err := generateCode()
			if err != nil {
				return nil, err
			}
			attempt.Code = generated
		}
		record, err := s.CreateCode(ctx, attempt)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// generateCode returns something a person can read off a screen and type
// without asking which character that was: no O against 0, no I or L against
// 1. Grouped in fours for the same reason a card number is.
func generateCode() (string, error) {
	const alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	const groups, size = 3, 4

	picked, err := drawSymbols(rand.Reader, groups*size, len(alphabet))
	if err != nil {
		return "", fmt.Errorf("card: generate code: %w", err)
	}

	var out strings.Builder
	for i, symbol := range picked {
		if i > 0 && i%size == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(alphabet[symbol])
	}
	return out.String(), nil
}

// drawSymbols returns count uniform indices into an alphabet width symbols
// wide, drawn from reader.
//
// Deliberately not `int(b) % width`: 256 is not a multiple of 31, so that
// spelling gives the first eight symbols of this alphabet one extra draw in
// every 256 — a favourite in a value whose entire job is to be unguessable,
// and free to remove. Bytes from ceiling up to 255 are rejected and drawn
// again instead. The reader is a parameter so a test can hand it a stream
// whose skewed bytes have to be skipped, which a statistical assertion over
// real randomness could only check by flaking eventually.
func drawSymbols(reader io.Reader, count, width int) ([]byte, error) {
	if width < 1 || width > 256 {
		return nil, fmt.Errorf("card: alphabet width %d", width)
	}

	// The largest multiple of width at or below 256. Every byte below it maps
	// onto the alphabet evenly; every byte at or above it is the remainder
	// that would not.
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

func (s *Store) CreateCode(ctx context.Context, in CodeInput) (Code, error) {
	if err := validateWindows(in.Windows); err != nil {
		return Code{}, err
	}
	rawWindows := normalizeWindows(in.Windows)
	record := Code{
		ID:        id.New(),
		Code:      strings.TrimSpace(in.Code),
		Name:      text.TrimAndTruncate(in.Name, MaxNameChars),
		Windows:   parseWindows(rawWindows),
		Cards:     in.Cards,
		CardDays:  clampDays(in.CardDays),
		ExpiresAt: in.ExpiresAt,
		Note:      text.TrimAndTruncate(in.Note, MaxNoteChars),
		CreatedAt: time.Now().UnixMilli(),
	}
	if record.Code == "" || len(record.Code) > MaxCodeChars {
		return Code{}, ErrInvalidCode
	}
	if record.Cards < 1 {
		return Code{}, ErrInvalidCount
	}
	record.Cards = min(record.Cards, MaxCards)

	_, err := s.db.Exec(ctx,
		`INSERT INTO redemption_codes
		 (id, code, name, windows, cards, claimed, card_days, expires_at, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Code, record.Name, rawWindows, record.Cards, 0, record.CardDays,
		record.ExpiresAt, record.Note, record.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Code{}, ErrCodeTaken
		}
		return Code{}, fmt.Errorf("card: create code: %w", err)
	}
	return record, nil
}

func (s *Store) ListCodes(ctx context.Context) ([]Code, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, code, name, windows, cards, claimed, card_days, expires_at, note, created_at
		 FROM redemption_codes ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("card: list codes: %w", err)
	}
	defer rows.Close()

	out := []Code{}
	for rows.Next() {
		var record Code
		var rawWins string
		if err := rows.Scan(&record.ID, &record.Code, &record.Name, &rawWins, &record.Cards, &record.Claimed,
			&record.CardDays, &record.ExpiresAt, &record.Note, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("card: scan code: %w", err)
		}
		record.Windows = parseWindows(rawWins)
		out = append(out, record)
	}
	return out, rows.Err()
}

// CodeRedemptions lists the current accounts that claimed a code, newest
// first. Deleted accounts are absent because account deletion deliberately
// cascades their redemption record along with the rest of their data.
func (s *Store) CodeRedemptions(ctx context.Context, codeID string) ([]Redemption, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.user_id, u.username, u.nickname, r.created_at
		FROM redemptions r
		JOIN users u ON u.id = r.user_id
		WHERE r.code_id = ?
		ORDER BY r.created_at DESC`, codeID)
	if err != nil {
		return nil, fmt.Errorf("card: list code redemptions: %w", err)
	}
	defer rows.Close()

	out := []Redemption{}
	for rows.Next() {
		var record Redemption
		if err := rows.Scan(&record.UserID, &record.Username, &record.Nickname, &record.RedeemedAt); err != nil {
			return nil, fmt.Errorf("card: scan code redemption: %w", err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("card: list code redemptions: %w", err)
	}
	return out, nil
}

func (s *Store) DeleteCode(ctx context.Context, codeID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM redemption_codes WHERE id = ?`, codeID); err != nil {
		return fmt.Errorf("card: delete code: %w", err)
	}
	return nil
}

// Redeem turns a typed string into a card, once per account.
//
// Both invariants are enforced by the database rather than by a check this
// code performs first: the primary key on redemptions refuses a second
// attempt by the same account, and the conditional UPDATE refuses the
// hundred-and-first claim on a hundred-card code. Under a burst of people
// pasting the same code at once, that is the difference between a limit and
// a suggestion.
func (s *Store) Redeem(ctx context.Context, userID, code string) (Card, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return Card{}, ErrInvalidCode
	}

	now := time.Now().UnixMilli()
	var card Card

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		var (
			codeID   string
			name     string
			rawWins  string
			cards    int
			claimed  int
			cardDays int
			expires  int64
		)
		err := tx.QueryRow(ctx,
			`SELECT id, name, windows, cards, claimed, card_days, expires_at FROM redemption_codes WHERE code = ?`,
			code).Scan(&codeID, &name, &rawWins, &cards, &claimed, &cardDays, &expires)
		if err != nil {
			if database.IsNotFound(err) {
				return ErrCodeUnknown
			}
			return fmt.Errorf("card: redeem: %w", err)
		}
		if expires != 0 && expires <= now {
			return ErrCodeExpired
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO redemptions (code_id, user_id, created_at) VALUES (?, ?, ?)`,
			codeID, userID, now); err != nil {
			if isUnique(err) {
				return ErrCodeUsed
			}
			return fmt.Errorf("card: redeem: %w", err)
		}

		result, err := tx.Exec(ctx,
			`UPDATE redemption_codes SET claimed = claimed + 1 WHERE id = ? AND claimed < cards`,
			codeID)
		if err != nil {
			return fmt.Errorf("card: redeem: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrCodeEmpty
		}

		card = Card{
			ID:        id.New(),
			Name:      name,
			Windows:   parseWindows(rawWins),
			Source:    SourceCode,
			ExpiresAt: now + int64(clampDays(cardDays))*24*3600*1000,
			CreatedAt: now,
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO usage_cards (id, user_id, name, windows, source, code_id, expires_at, used_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			card.ID, userID, card.Name, rawWins, card.Source, codeID, card.ExpiresAt, 0, card.CreatedAt); err != nil {
			return fmt.Errorf("card: redeem: %w", err)
		}
		return nil
	})
	if err != nil {
		return Card{}, err
	}
	return card, nil
}

func clampDays(days int) int {
	if days < 1 {
		return 30
	}
	return min(days, MaxDays)
}

func isUnique(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate") ||
		strings.Contains(message, "constraint")
}
