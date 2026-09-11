// Package security stores security decisions and the small amount of state
// needed to challenge unusually fast browser chat traffic.
package security

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityDanger  Severity = "danger"
)

// Event is one security decision. Account, actor, and address names are
// snapshots because the event must remain intelligible after either account
// is renamed or deleted.
type Event struct {
	ID            string   `json:"id"`
	At            int64    `json:"at"`
	Event         string   `json:"event"`
	Severity      Severity `json:"severity"`
	UserID        string   `json:"user_id,omitempty"`
	Username      string   `json:"username,omitempty"`
	ActorID       string   `json:"actor_id,omitempty"`
	ActorUsername string   `json:"actor_username,omitempty"`
	IP            string   `json:"ip,omitempty"`
	Source        string   `json:"source,omitempty"`
	Decision      string   `json:"decision,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

const (
	EventSignupReview       = "signup_review"
	EventAPIRestriction     = "api_restriction"
	EventAPIRestrictionLift = "api_restriction_lifted"
	EventChatChallenge      = "chat_challenge"

	maxReasonChars = 500
	MaxPageSize    = 200
)

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) Record(ctx context.Context, q database.Queryer, event Event) error {
	if q == nil {
		q = s.db
	}
	if event.ID == "" {
		event.ID = id.New()
	}
	if event.At == 0 {
		event.At = time.Now().UnixMilli()
	}
	event.Event = text.TrimAndTruncate(strings.TrimSpace(event.Event), 80)
	event.Username = text.TrimAndTruncate(strings.TrimSpace(event.Username), 80)
	event.ActorUsername = text.TrimAndTruncate(strings.TrimSpace(event.ActorUsername), 80)
	event.IP = text.TrimAndTruncate(strings.TrimSpace(event.IP), 80)
	event.Source = text.TrimAndTruncate(strings.TrimSpace(event.Source), 80)
	event.Decision = text.TrimAndTruncate(strings.TrimSpace(event.Decision), 40)
	event.Reason = text.TrimAndTruncate(strings.TrimSpace(event.Reason), maxReasonChars)
	if event.Event == "" {
		return fmt.Errorf("security: event type is required")
	}
	if event.Severity == "" {
		event.Severity = SeverityInfo
	}

	_, err := q.Exec(ctx, `INSERT INTO security_events
		(id, at, event, severity, user_id, username, actor_id, actor_username,
		 ip, source, decision, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.At, event.Event, event.Severity, event.UserID,
		event.Username, event.ActorID, event.ActorUsername, event.IP,
		event.Source, event.Decision, event.Reason)
	if err != nil {
		return fmt.Errorf("security: record event: %w", err)
	}
	return nil
}

type Filter struct {
	UserID   string
	Event    string
	Severity Severity
	Decision string
	Since    int64
	Limit    int
	Offset   int
}

type ChatPolicy struct {
	Requests  int
	Window    time.Duration
	Clearance time.Duration
}

type ChatAttemptState struct {
	Required bool
	Attempt  int
}

func (s *Store) List(ctx context.Context, filter Filter) ([]Event, int64, error) {
	where, args := filter.where()
	var total int64
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM security_events`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("security: count events: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 || limit > MaxPageSize {
		limit = 50
	}
	offset := max(0, filter.Offset)
	rows, err := s.db.Query(ctx,
		`SELECT id, at, event, severity, user_id, username, actor_id, actor_username,
		        ip, source, decision, reason
		 FROM security_events`+where+` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("security: list events: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.At, &event.Event, &event.Severity,
			&event.UserID, &event.Username, &event.ActorID, &event.ActorUsername,
			&event.IP, &event.Source,
			&event.Decision, &event.Reason); err != nil {
			return nil, 0, fmt.Errorf("security: scan event: %w", err)
		}
		events = append(events, event)
	}
	return events, total, rows.Err()
}

func (f Filter) where() (string, []any) {
	conditions := []string{}
	args := []any{}
	add := func(clause string, value any) {
		conditions = append(conditions, clause)
		args = append(args, value)
	}
	if f.UserID != "" {
		add("user_id = ?", f.UserID)
	}
	if f.Event != "" {
		add("event = ?", f.Event)
	}
	if f.Severity != "" {
		add("severity = ?", f.Severity)
	}
	if f.Decision != "" {
		add("decision = ?", f.Decision)
	}
	if f.Since > 0 {
		add("at >= ?", f.Since)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// ChatAttempt records one browser turn and reports its place in the current
// burst. A recent successful challenge suppresses the check until its
// clearance expires.
func (s *Store) ChatAttempt(
	ctx context.Context, userID string, now time.Time, requests int, window time.Duration,
) (ChatAttemptState, error) {
	if requests <= 0 || window <= 0 {
		return ChatAttemptState{}, nil
	}

	state := ChatAttemptState{}
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID)
		if err != nil {
			return fmt.Errorf("security: lock chat account: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return fmt.Errorf("security: chat account not found")
		}

		var start, verified int64
		var attempts int
		err = tx.QueryRow(ctx,
			`SELECT window_start, attempts, verified_until FROM chat_security_state WHERE user_id = ?`,
			userID).Scan(&start, &attempts, &verified)
		if database.IsNotFound(err) {
			state.Attempt = 1
			_, err = tx.Exec(ctx,
				`INSERT INTO chat_security_state (user_id, window_start, attempts, verified_until)
				 VALUES (?, ?, 1, 0)`, userID, now.UnixMilli())
			return err
		}
		if err != nil {
			return fmt.Errorf("security: load chat state: %w", err)
		}
		if verified > now.UnixMilli() {
			state.Attempt = attempts
			return nil
		}

		if start+window.Milliseconds() <= now.UnixMilli() {
			start = now.UnixMilli()
			attempts = 1
		} else {
			attempts++
		}
		state.Attempt = attempts
		state.Required = attempts > requests
		_, err = tx.Exec(ctx,
			`UPDATE chat_security_state SET window_start = ?, attempts = ? WHERE user_id = ?`,
			start, attempts, userID)
		return err
	})
	if err != nil {
		return ChatAttemptState{}, err
	}
	return state, nil
}

func (s *Store) ClearChatChallenge(ctx context.Context, userID string, until time.Time) error {
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("security: lock chat account: %w", err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO chat_security_state (user_id, window_start, attempts, verified_until)
			 VALUES (?, 0, 0, ?)
			 ON CONFLICT (user_id) DO UPDATE SET window_start = 0, attempts = 0, verified_until = ?`,
			userID, until.UnixMilli(), until.UnixMilli())
		return err
	})
	if err != nil {
		return fmt.Errorf("security: clear chat challenge: %w", err)
	}
	return nil
}
