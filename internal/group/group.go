// Package group owns user groups: the unit an administrator uses to say
// which models a set of people may reach and what quota they share.
//
// Nothing here is hard-coded. There is no Free or Pro in the source; an
// instance starts with one group called Default and an administrator makes
// whatever others they need.
package group

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

type Group struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// New accounts join this group. Exactly one row carries it.
	IsDefault bool `json:"is_default"`
	// "every enabled model", so adding a model does not mean revisiting every
	// group that should obviously have it.
	AllowAllModels bool `json:"allow_all_models"`
	// Whether members may reach the instance over the API rather than the
	// browser. Meaningless while the operator has the API switched off
	// instance-wide; this narrows that switch, it does not stand in for it.
	APIAccess bool  `json:"api_access"`
	SortOrder int   `json:"sort_order"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

var (
	ErrNotFound      = errors.New("group: not found")
	ErrNameTaken     = errors.New("group: a group with that name already exists")
	ErrInvalidName   = errors.New("group: name must be 1-40 characters")
	ErrDeleteLast    = errors.New("group: the last group cannot be deleted")
	ErrDeleteDefault = errors.New("group: the default group cannot be deleted; make another group the default first")
)

const (
	MaxNameChars        = 40
	MaxDescriptionChars = 200
)

const columns = `id, name, description, is_default, allow_all_models, api_access, sort_order, created_at, updated_at`

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

type CreateInput struct {
	Name           string
	Description    string
	IsDefault      bool
	AllowAllModels bool
	// Whether members may reach the instance over the API. The column
	// defaults to true so that existing groups keep working when the
	// instance-wide switch is turned on; a group created through this struct
	// says so explicitly, and the admin form ticks the box by default to
	// match.
	APIAccess bool
	SortOrder int
}

func (s *Store) Create(ctx context.Context, q database.Queryer, in CreateInput) (Group, error) {
	if q == nil {
		q = s.db
	}
	name, err := checkName(in.Name)
	if err != nil {
		return Group{}, err
	}

	now := time.Now().UnixMilli()
	record := Group{
		ID:             id.New(),
		Name:           name,
		Description:    text.TrimAndTruncate(in.Description, MaxDescriptionChars),
		IsDefault:      in.IsDefault,
		AllowAllModels: in.AllowAllModels,
		APIAccess:      in.APIAccess,
		SortOrder:      in.SortOrder,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	_, err = q.Exec(ctx, `INSERT INTO user_groups (`+columns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Name, record.Description, record.IsDefault, record.AllowAllModels,
		record.APIAccess, record.SortOrder, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameTaken
		}
		return Group{}, fmt.Errorf("group: create: %w", err)
	}
	if record.IsDefault {
		if err := s.clearOtherDefaults(ctx, q, record.ID); err != nil {
			return Group{}, err
		}
	}
	return record, nil
}

func (s *Store) ByID(ctx context.Context, q database.Queryer, groupID string) (Group, error) {
	if q == nil {
		q = s.db
	}
	return scan(q.QueryRow(ctx, `SELECT `+columns+` FROM user_groups WHERE id = ?`, groupID))
}

// Default is the group a new registration lands in. It is also the fallback
// for a user whose group was deleted out from under them.
func (s *Store) Default(ctx context.Context, q database.Queryer) (Group, error) {
	if q == nil {
		q = s.db
	}
	record, err := scan(q.QueryRow(ctx,
		`SELECT `+columns+` FROM user_groups WHERE is_default = ? ORDER BY sort_order, id LIMIT 1`, true))
	if err == nil || !errors.Is(err, ErrNotFound) {
		return record, err
	}
	// No group is marked default (an administrator unset it, or an old
	// database). Falling back to the first group beats refusing to register.
	return scan(q.QueryRow(ctx, `SELECT `+columns+` FROM user_groups ORDER BY sort_order, id LIMIT 1`))
}

func (s *Store) List(ctx context.Context, q database.Queryer) ([]Group, error) {
	if q == nil {
		q = s.db
	}
	rows, err := q.Query(ctx, `SELECT `+columns+` FROM user_groups ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("group: list: %w", err)
	}
	defer rows.Close()

	out := []Group{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) Count(ctx context.Context, q database.Queryer) (int, error) {
	if q == nil {
		q = s.db
	}
	var count int
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM user_groups`).Scan(&count); err != nil {
		return 0, fmt.Errorf("group: count: %w", err)
	}
	return count, nil
}

type Update struct {
	Name           *string
	Description    *string
	IsDefault      *bool
	AllowAllModels *bool
	APIAccess      *bool
	SortOrder      *int
}

func (s *Store) Update(ctx context.Context, q database.Queryer, groupID string, in Update) (Group, error) {
	if q == nil {
		q = s.db
	}
	sets := []string{}
	args := []any{}

	if in.Name != nil {
		name, err := checkName(*in.Name)
		if err != nil {
			return Group{}, err
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if in.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, text.TrimAndTruncate(*in.Description, MaxDescriptionChars))
	}
	if in.IsDefault != nil {
		sets = append(sets, "is_default = ?")
		args = append(args, *in.IsDefault)
	}
	if in.AllowAllModels != nil {
		sets = append(sets, "allow_all_models = ?")
		args = append(args, *in.AllowAllModels)
	}
	if in.APIAccess != nil {
		sets = append(sets, "api_access = ?")
		args = append(args, *in.APIAccess)
	}
	if in.SortOrder != nil {
		sets = append(sets, "sort_order = ?")
		args = append(args, *in.SortOrder)
	}
	if len(sets) == 0 {
		return s.ByID(ctx, q, groupID)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UnixMilli(), groupID)

	if _, err := q.Exec(ctx, `UPDATE user_groups SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		if isUnique(err) {
			return Group{}, ErrNameTaken
		}
		return Group{}, fmt.Errorf("group: update: %w", err)
	}
	if in.IsDefault != nil && *in.IsDefault {
		if err := s.clearOtherDefaults(ctx, q, groupID); err != nil {
			return Group{}, err
		}
	}
	return s.ByID(ctx, q, groupID)
}

func (s *Store) Delete(ctx context.Context, q database.Queryer, groupID string) error {
	if q == nil {
		q = s.db
	}
	if _, err := q.Exec(ctx, `DELETE FROM user_groups WHERE id = ?`, groupID); err != nil {
		return fmt.Errorf("group: delete: %w", err)
	}
	return nil
}

// Exactly one default is an invariant maintained here rather than by a
// constraint, because switching which group is default is a two-row change
// and a partial unique index would reject the intermediate state.
func (s *Store) clearOtherDefaults(ctx context.Context, q database.Queryer, keep string) error {
	_, err := q.Exec(ctx, `UPDATE user_groups SET is_default = ?, updated_at = ? WHERE id <> ? AND is_default = ?`,
		false, time.Now().UnixMilli(), keep, true)
	if err != nil {
		return fmt.Errorf("group: clear defaults: %w", err)
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner) (Group, error) {
	var record Group
	err := row.Scan(&record.ID, &record.Name, &record.Description, &record.IsDefault,
		&record.AllowAllModels, &record.APIAccess, &record.SortOrder, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Group{}, ErrNotFound
		}
		return Group{}, fmt.Errorf("group: scan: %w", err)
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

func isUnique(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
