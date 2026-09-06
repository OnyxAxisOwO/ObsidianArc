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
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

const (
	SourceGrant = "grant"
	SourceCode  = "code"

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
	ErrNotFound     = errors.New("card: not found")
	ErrUsed         = errors.New("card: already used")
	ErrExpired      = errors.New("card: expired")
	ErrCodeUnknown  = errors.New("card: no such code")
	ErrCodeExpired  = errors.New("card: that code has expired")
	ErrCodeEmpty    = errors.New("card: that code has been fully redeemed")
	ErrCodeUsed     = errors.New("card: this account has already redeemed that code")
	ErrCodeTaken    = errors.New("card: that code already exists")
	ErrInvalidCode  = errors.New("card: a code is required")
	ErrInvalidCount = errors.New("card: at least one card is required")
	ErrNamedBatch   = errors.New("card: a batch is generated, so it cannot be given a code of its own")
)

// Card is one reset, as its owner sees it.
type Card struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	ExpiresAt int64  `json:"expires_at"`
	UsedAt    int64  `json:"used_at,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// Code is a batch of cards behind a string somebody types in.
type Code struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Cards     int    `json:"cards"`
	Claimed   int    `json:"claimed"`
	CardDays  int    `json:"card_days"`
	ExpiresAt int64  `json:"expires_at"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

// --- cards ---------------------------------------------------------------------

// Available is what an account may still spend: unused, and not yet expired.
func (s *Store) Available(ctx context.Context, userID string) ([]Card, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, source, expires_at, used_at, created_at FROM usage_cards
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
		if err := rows.Scan(&record.ID, &record.Source, &record.ExpiresAt,
			&record.UsedAt, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("card: scan: %w", err)
		}
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
	now := time.Now().UnixMilli()
	result, err := s.db.Exec(ctx,
		`UPDATE usage_cards SET used_at = ?
		 WHERE id = ? AND user_id = ? AND used_at = ? AND expires_at > ?`,
		now, cardID, userID, 0, now)
	if err != nil {
		return fmt.Errorf("card: spend: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 1 {
		return nil
	}

	// Nothing changed. Say which of the three reasons it was, because "that
	// card is gone" and "that card was never yours" want different answers.
	var used, expires int64
	err = s.db.QueryRow(ctx,
		`SELECT used_at, expires_at FROM usage_cards WHERE id = ? AND user_id = ?`,
		cardID, userID).Scan(&used, &expires)
	if err != nil {
		if database.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("card: spend: %w", err)
	}
	if used != 0 {
		return ErrUsed
	}
	return ErrExpired
}

// Grant hands cards to one account without a code in between.
func (s *Store) Grant(ctx context.Context, userID string, count, days int) ([]Card, error) {
	if count < 1 {
		return nil, ErrInvalidCount
	}
	count = min(count, MaxCards)
	days = clampDays(days)

	now := time.Now().UnixMilli()
	expires := now + int64(days)*24*3600*1000

	out := make([]Card, 0, count)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		for range count {
			record := Card{
				ID: id.New(), Source: SourceGrant,
				ExpiresAt: expires, CreatedAt: now,
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO usage_cards (id, user_id, source, code_id, expires_at, used_at, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				record.ID, userID, record.Source, "", record.ExpiresAt, 0, record.CreatedAt); err != nil {
				return fmt.Errorf("card: grant: %w", err)
			}
			out = append(out, record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// --- codes ---------------------------------------------------------------------

type CodeInput struct {
	Code      string
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

	buf := make([]byte, groups*size)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("card: generate code: %w", err)
	}

	var out strings.Builder
	for i, b := range buf {
		if i > 0 && i%size == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(alphabet[int(b)%len(alphabet)])
	}
	return out.String(), nil
}

func (s *Store) CreateCode(ctx context.Context, in CodeInput) (Code, error) {
	record := Code{
		ID:        id.New(),
		Code:      strings.TrimSpace(in.Code),
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
		 (id, code, cards, claimed, card_days, expires_at, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Code, record.Cards, 0, record.CardDays,
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
		`SELECT id, code, cards, claimed, card_days, expires_at, note, created_at
		 FROM redemption_codes ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("card: list codes: %w", err)
	}
	defer rows.Close()

	out := []Code{}
	for rows.Next() {
		var record Code
		if err := rows.Scan(&record.ID, &record.Code, &record.Cards, &record.Claimed,
			&record.CardDays, &record.ExpiresAt, &record.Note, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("card: scan code: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
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
			cards    int
			claimed  int
			cardDays int
			expires  int64
		)
		err := tx.QueryRow(ctx,
			`SELECT id, cards, claimed, card_days, expires_at FROM redemption_codes WHERE code = ?`,
			code).Scan(&codeID, &cards, &claimed, &cardDays, &expires)
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
			Source:    SourceCode,
			ExpiresAt: now + int64(clampDays(cardDays))*24*3600*1000,
			CreatedAt: now,
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO usage_cards (id, user_id, source, code_id, expires_at, used_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			card.ID, userID, card.Source, codeID, card.ExpiresAt, 0, card.CreatedAt); err != nil {
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
