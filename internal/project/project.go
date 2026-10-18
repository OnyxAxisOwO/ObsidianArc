// Package project is a standing context a conversation can be started in:
// a name and a prompt the conversations inside it inherit.
//
// Every read and every write is scoped by owner, and there is no way to
// reach a project that belongs to somebody else. That is not tidiness. A
// project's instructions become part of the system prompt of every
// conversation started in it, and those conversations run with the reader's
// own permissions. A project somebody else wrote would be somebody else
// writing instructions for the reader's model. There is no sharing in this
// version, and the owner scope on every query is what keeps it that way until
// the question is asked deliberately.
package project

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

const (
	// Generous for a person and small enough that the rail stays a list
	// somebody reads rather than a thing they search.
	MaxPerUser = 64

	MaxNameChars = 80
	// Room for a real brief. The instance prompt and the model's own prompt
	// are already ahead of it in the chain, and the whole thing is billed as
	// input tokens on every turn of a session, which is the real ceiling.
	MaxInstructionsChars = 8000
)

var (
	ErrNotFound         = errors.New("project: not found")
	ErrTooMany          = errors.New("project: this account already holds the maximum number of projects")
	ErrNameRequired     = errors.New("project: a project needs a name")
	ErrNameTooLong      = errors.New("project: that name is too long")
	ErrInstructionsLong = errors.New("project: those instructions are too long")
)

type Project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	// How many conversations sit in it, for the rail.
	Conversations int   `json:"conversations"`
	CreatedAt     int64 `json:"created_at"`
	UpdatedAt     int64 `json:"updated_at"`
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const columns = `p.id, p.name, p.instructions, p.created_at, p.updated_at,
	(SELECT COUNT(*) FROM conversations c WHERE c.project_id = p.id) AS conversations`

// Create makes one project for userID.
//
// The cap check and the insert share a transaction that locks the owner's
// row first, for the reason apikey.Store.Issue does the same: a count
// followed by a standalone insert lets two parallel requests both see the
// last free slot and take it.
func (s *Store) Create(ctx context.Context, userID, name, instructions string) (Project, error) {
	clean, err := checkName(name)
	if err != nil {
		return Project{}, err
	}
	body, err := checkInstructions(instructions)
	if err != nil {
		return Project{}, err
	}

	now := time.Now().UnixMilli()
	record := Project{
		ID: id.New(), Name: clean, Instructions: body,
		CreatedAt: now, UpdatedAt: now,
	}

	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, userID)
		if err != nil {
			return fmt.Errorf("project: lock owner: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return ErrNotFound
		}

		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM projects WHERE user_id = ?`, userID).Scan(&count); err != nil {
			return fmt.Errorf("project: count: %w", err)
		}
		if count >= MaxPerUser {
			return ErrTooMany
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO projects (id, user_id, name, instructions, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			record.ID, userID, record.Name, record.Instructions, record.CreatedAt, record.UpdatedAt)
		if err != nil {
			return fmt.Errorf("project: create: %w", err)
		}
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	return record, nil
}

// List is every project a user owns, most recently changed first. There are
// at most MaxPerUser of them, so this does not page.
func (s *Store) List(ctx context.Context, userID string) ([]Project, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+columns+` FROM projects p WHERE p.user_id = ? ORDER BY p.updated_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("project: list: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		var record Project
		if err := rows.Scan(&record.ID, &record.Name, &record.Instructions,
			&record.CreatedAt, &record.UpdatedAt, &record.Conversations); err != nil {
			return nil, fmt.Errorf("project: scan: %w", err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("project: list: %w", err)
	}
	return out, nil
}

// Get is one project, scoped by owner. q may be nil for a standalone read.
func (s *Store) Get(ctx context.Context, q database.Queryer, userID, projectID string) (Project, error) {
	if q == nil {
		q = s.db
	}
	var record Project
	err := q.QueryRow(ctx,
		`SELECT `+columns+` FROM projects p WHERE p.id = ? AND p.user_id = ?`, projectID, userID).
		Scan(&record.ID, &record.Name, &record.Instructions,
			&record.CreatedAt, &record.UpdatedAt, &record.Conversations)
	if errors.Is(err, database.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("project: get: %w", err)
	}
	return record, nil
}

type Update struct {
	Name         *string
	Instructions *string
}

func (s *Store) Update(ctx context.Context, userID, projectID string, in Update) (Project, error) {
	sets := []string{}
	args := []any{}

	if in.Name != nil {
		clean, err := checkName(*in.Name)
		if err != nil {
			return Project{}, err
		}
		sets = append(sets, "name = ?")
		args = append(args, clean)
	}
	if in.Instructions != nil {
		body, err := checkInstructions(*in.Instructions)
		if err != nil {
			return Project{}, err
		}
		sets = append(sets, "instructions = ?")
		args = append(args, body)
	}
	if len(sets) == 0 {
		return s.Get(ctx, nil, userID, projectID)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UnixMilli(), projectID, userID)

	result, err := s.db.Exec(ctx,
		`UPDATE projects SET `+strings.Join(sets, ", ")+` WHERE id = ? AND user_id = ?`, args...)
	if err != nil {
		return Project{}, fmt.Errorf("project: update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Project{}, fmt.Errorf("project: update rows: %w", err)
	}
	if affected == 0 {
		return Project{}, ErrNotFound
	}
	return s.Get(ctx, nil, userID, projectID)
}

// Delete removes the project. Its conversations survive with project_id set
// to null by the schema — deleting a project is a decision about the
// project, and taking a year of transcripts with it is not what anybody
// means by it.
func (s *Store) Delete(ctx context.Context, userID, projectID string) error {
	result, err := s.db.Exec(ctx,
		`DELETE FROM projects WHERE id = ? AND user_id = ?`, projectID, userID)
	if err != nil {
		return fmt.Errorf("project: delete: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("project: delete rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func checkName(name string) (string, error) {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return "", ErrNameRequired
	}
	if utf8.RuneCountInString(clean) > MaxNameChars {
		return "", ErrNameTooLong
	}
	return clean, nil
}

func checkInstructions(instructions string) (string, error) {
	body := strings.TrimSpace(instructions)
	if utf8.RuneCountInString(body) > MaxInstructionsChars {
		return "", ErrInstructionsLong
	}
	return body, nil
}
