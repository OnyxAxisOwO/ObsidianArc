// Package conversation owns the transcript: conversations, their messages,
// and the images attached to them.
//
// Every read and every write is scoped by the owning user in its WHERE
// clause. That is the shape rather than a convention — a method that takes a
// conversation id without a user id does not exist here, so an insecure
// direct object reference cannot be written by forgetting a check.
package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Conversation struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	ModelID      string `json:"model_id"`
	Pinned       bool   `json:"pinned"`
	MessageCount int    `json:"message_count"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Stats is what the turn cost and how fast it was, shown under the answer.
// Every field is optional because plenty of endpoints report no token counts
// at all.
type Stats struct {
	MS              int64    `json:"ms"`
	FirstTokenMS    *int64   `json:"first_token_ms,omitempty"`
	Streamed        bool     `json:"streamed"`
	InputTokens     *int     `json:"input_tokens,omitempty"`
	OutputTokens    *int     `json:"output_tokens,omitempty"`
	ReasoningTokens *int     `json:"reasoning_tokens,omitempty"`
	TPS             *float64 `json:"tps,omitempty"`
}

type Message struct {
	ID        string `json:"id"`
	Seq       int    `json:"seq"`
	Role      Role   `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
	Error     string `json:"error,omitempty"`
	ModelID   string `json:"model_id,omitempty"`
	// Names the model as it was when the message was written, so a renamed or
	// deleted model does not leave old turns unattributed.
	ModelName   string       `json:"model_name,omitempty"`
	Stats       *Stats       `json:"stats,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   int64        `json:"created_at"`
}

type Attachment struct {
	ID     string `json:"id"`
	Mime   string `json:"mime"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int    `json:"size"`
}

var (
	ErrNotFound        = errors.New("conversation: not found")
	ErrMessageNotFound = errors.New("conversation: no such message")
	ErrTitleTooLong    = errors.New("conversation: title must be 120 characters or fewer")
)

const (
	MaxTitleChars = 120
	// The transcript is what gets re-sent on every turn, so an unbounded
	// message is an unbounded per-turn cost.
	MaxContentChars   = 32000
	MaxReasoningChars = 60000
	MaxErrorChars     = 500
	// How many conversations the rail lists.
	DefaultListLimit = 60
	MaxListLimit     = 200
)

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const conversationColumns = `id, title, model_id, pinned, message_count, created_at, updated_at`

// --- conversations --------------------------------------------------------

func (s *Store) Create(ctx context.Context, q database.Queryer, userID, title, modelID string) (Conversation, error) {
	if q == nil {
		q = s.db
	}
	now := time.Now().UnixMilli()
	record := Conversation{
		ID:        id.New(),
		Title:     truncate(title, MaxTitleChars),
		ModelID:   modelID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := q.Exec(ctx,
		`INSERT INTO conversations (id, user_id, title, model_id, pinned, message_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
		record.ID, userID, record.Title, nullable(record.ModelID), false, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return Conversation{}, fmt.Errorf("conversation: create: %w", err)
	}
	return record, nil
}

func (s *Store) List(ctx context.Context, userID string, limit int) ([]Conversation, error) {
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+conversationColumns+` FROM conversations
		 WHERE user_id = ? ORDER BY pinned DESC, updated_at DESC, id DESC LIMIT ?`,
		userID, limit)
	if err != nil {
		return nil, fmt.Errorf("conversation: list: %w", err)
	}
	defer rows.Close()

	out := []Conversation{}
	for rows.Next() {
		record, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, q database.Queryer, userID, conversationID string) (Conversation, error) {
	if q == nil {
		q = s.db
	}
	return scanConversation(q.QueryRow(ctx,
		`SELECT `+conversationColumns+` FROM conversations WHERE id = ? AND user_id = ?`,
		conversationID, userID))
}

type Update struct {
	Title  *string
	Pinned *bool
}

func (s *Store) Update(ctx context.Context, userID, conversationID string, in Update) (Conversation, error) {
	sets := []string{}
	args := []any{}

	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if utf8.RuneCountInString(title) > MaxTitleChars {
			return Conversation{}, ErrTitleTooLong
		}
		sets = append(sets, "title = ?")
		args = append(args, title)
	}
	if in.Pinned != nil {
		sets = append(sets, "pinned = ?")
		args = append(args, *in.Pinned)
	}
	if len(sets) == 0 {
		return s.Get(ctx, nil, userID, conversationID)
	}

	// updated_at is deliberately not touched: renaming a conversation should
	// not reorder the rail.
	args = append(args, conversationID, userID)
	result, err := s.db.Exec(ctx,
		`UPDATE conversations SET `+strings.Join(sets, ", ")+` WHERE id = ? AND user_id = ?`, args...)
	if err != nil {
		return Conversation{}, fmt.Errorf("conversation: update: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Conversation{}, ErrNotFound
	}
	return s.Get(ctx, nil, userID, conversationID)
}

func (s *Store) Delete(ctx context.Context, userID, conversationID string) error {
	result, err := s.db.Exec(ctx, `DELETE FROM conversations WHERE id = ? AND user_id = ?`,
		conversationID, userID)
	if err != nil {
		return fmt.Errorf("conversation: delete: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAll(ctx context.Context, userID string) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM conversations WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("conversation: delete all: %w", err)
	}
	removed, _ := result.RowsAffected()
	return removed, nil
}

// --- messages --------------------------------------------------------------

const messageColumns = `m.id, m.seq, m.role, m.content, m.reasoning, m.error, m.model_id,
	m.model_name, m.stats_json, m.created_at`

// Messages returns a conversation's transcript in order, with attachments
// attached. Two queries rather than a join with fan-out, so a conversation
// with many images does not multiply every message row by its pictures.
func (s *Store) Messages(ctx context.Context, q database.Queryer, userID, conversationID string) ([]Message, error) {
	if q == nil {
		q = s.db
	}
	rows, err := q.Query(ctx,
		`SELECT `+messageColumns+`
		 FROM messages m
		 WHERE m.conversation_id = ? AND m.user_id = ?
		 ORDER BY m.seq`,
		conversationID, userID)
	if err != nil {
		return nil, fmt.Errorf("conversation: messages: %w", err)
	}
	defer rows.Close()

	messages := []Message{}
	index := map[string]int{}
	for rows.Next() {
		record, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		index[record.ID] = len(messages)
		messages = append(messages, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return messages, nil
	}

	attachments, err := q.Query(ctx,
		`SELECT a.id, a.message_id, a.mime, a.width, a.height, a.size
		 FROM attachments a
		 JOIN messages m ON m.id = a.message_id
		 WHERE m.conversation_id = ? AND a.user_id = ?
		 ORDER BY a.created_at`,
		conversationID, userID)
	if err != nil {
		return nil, fmt.Errorf("conversation: attachments: %w", err)
	}
	defer attachments.Close()

	for attachments.Next() {
		var (
			record    Attachment
			messageID string
		)
		if err := attachments.Scan(&record.ID, &messageID, &record.Mime,
			&record.Width, &record.Height, &record.Size); err != nil {
			return nil, fmt.Errorf("conversation: attachment scan: %w", err)
		}
		if position, ok := index[messageID]; ok {
			messages[position].Attachments = append(messages[position].Attachments, record)
		}
	}
	return messages, attachments.Err()
}

// AppendInput is one message to add. Seq is assigned by the store so two
// concurrent turns cannot land on the same position.
type AppendInput struct {
	ConversationID string
	UserID         string
	Role           Role
	Content        string
	Reasoning      string
	Error          string
	ModelID        string
	// The model's name at the time. Stored rather than joined, so deleting a
	// model does not unattribute every answer it ever gave.
	ModelName     string
	ProviderID    string
	Stats         *Stats
	AttachmentIDs []string
}

func (s *Store) Append(ctx context.Context, q database.Queryer, in AppendInput) (Message, error) {
	if q == nil {
		q = s.db
	}

	var next int
	// COALESCE rather than a separate "is this the first message" branch.
	err := q.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE conversation_id = ?`,
		in.ConversationID).Scan(&next)
	if err != nil {
		return Message{}, fmt.Errorf("conversation: next seq: %w", err)
	}

	record := Message{
		ID:        id.New(),
		Seq:       next,
		Role:      in.Role,
		Content:   truncate(in.Content, MaxContentChars),
		Reasoning: truncate(in.Reasoning, MaxReasoningChars),
		Error:     truncate(in.Error, MaxErrorChars),
		ModelID:   in.ModelID,
		ModelName: in.ModelName,
		Stats:     in.Stats,
		CreatedAt: time.Now().UnixMilli(),
	}

	stats := ""
	if in.Stats != nil {
		encoded, err := json.Marshal(in.Stats)
		if err != nil {
			return Message{}, fmt.Errorf("conversation: encode stats: %w", err)
		}
		stats = string(encoded)
	}

	_, err = q.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, user_id, seq, role, content, reasoning,
		 error, model_id, model_name, provider_id, stats_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, in.ConversationID, in.UserID, record.Seq, record.Role, record.Content,
		record.Reasoning, record.Error, nullable(in.ModelID), record.ModelName,
		nullable(in.ProviderID), stats, record.CreatedAt)
	if err != nil {
		return Message{}, fmt.Errorf("conversation: append: %w", err)
	}

	if len(in.AttachmentIDs) > 0 {
		attached, err := s.linkAttachments(ctx, q, in.UserID, record.ID, in.AttachmentIDs)
		if err != nil {
			return Message{}, err
		}
		record.Attachments = attached
	}

	if err := s.touch(ctx, q, in.ConversationID, in.UserID); err != nil {
		return Message{}, err
	}
	return record, nil
}

// TruncateFrom removes a message and everything after it. This is what makes
// editing an earlier turn and regenerating an answer the same operation: the
// replies to a question that no longer exists go with it.
func (s *Store) TruncateFrom(ctx context.Context, q database.Queryer, userID, conversationID, messageID string) error {
	if q == nil {
		q = s.db
	}

	var seq int
	err := q.QueryRow(ctx,
		`SELECT seq FROM messages WHERE id = ? AND conversation_id = ? AND user_id = ?`,
		messageID, conversationID, userID).Scan(&seq)
	if err != nil {
		if database.IsNotFound(err) {
			return ErrMessageNotFound
		}
		return fmt.Errorf("conversation: locate message: %w", err)
	}

	if _, err := q.Exec(ctx,
		`DELETE FROM messages WHERE conversation_id = ? AND user_id = ? AND seq >= ?`,
		conversationID, userID, seq); err != nil {
		return fmt.Errorf("conversation: truncate: %w", err)
	}
	return s.touch(ctx, q, conversationID, userID)
}

// touch keeps message_count and updated_at correct after any change. Counting
// rather than incrementing, because a truncate removes an unknown number.
func (s *Store) touch(ctx context.Context, q database.Queryer, conversationID, userID string) error {
	_, err := q.Exec(ctx,
		`UPDATE conversations
		 SET updated_at = ?,
		     message_count = (SELECT COUNT(*) FROM messages WHERE conversation_id = ?)
		 WHERE id = ? AND user_id = ?`,
		time.Now().UnixMilli(), conversationID, conversationID, userID)
	if err != nil {
		return fmt.Errorf("conversation: touch: %w", err)
	}
	return nil
}

// SetModel records which model a conversation last used.
func (s *Store) SetModel(ctx context.Context, q database.Queryer, userID, conversationID, modelID string) error {
	if q == nil {
		q = s.db
	}
	_, err := q.Exec(ctx, `UPDATE conversations SET model_id = ? WHERE id = ? AND user_id = ?`,
		nullable(modelID), conversationID, userID)
	if err != nil {
		return fmt.Errorf("conversation: set model: %w", err)
	}
	return nil
}

// SetTitle is used once per conversation, when the first turn names it.
func (s *Store) SetTitle(ctx context.Context, q database.Queryer, userID, conversationID, title string) error {
	if q == nil {
		q = s.db
	}
	_, err := q.Exec(ctx, `UPDATE conversations SET title = ? WHERE id = ? AND user_id = ? AND title = ''`,
		truncate(title, MaxTitleChars), conversationID, userID)
	if err != nil {
		return fmt.Errorf("conversation: set title: %w", err)
	}
	return nil
}

// DeriveTitle names a conversation by its opening line, which is what a user
// recognises it by. Stored rather than derived on read, so a rename sticks.
func DeriveTitle(content string) string {
	collapsed := strings.Join(strings.Fields(content), " ")
	return truncate(collapsed, 60)
}

// --- scanning ---------------------------------------------------------------

type rowScanner interface{ Scan(dest ...any) error }

func scanConversation(row rowScanner) (Conversation, error) {
	var (
		record  Conversation
		modelID sql.NullString
	)
	err := row.Scan(&record.ID, &record.Title, &modelID, &record.Pinned,
		&record.MessageCount, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Conversation{}, ErrNotFound
		}
		return Conversation{}, fmt.Errorf("conversation: scan: %w", err)
	}
	record.ModelID = modelID.String
	return record, nil
}

func scanMessage(row rowScanner) (Message, error) {
	var (
		record  Message
		modelID sql.NullString
		stats   string
	)
	err := row.Scan(&record.ID, &record.Seq, &record.Role, &record.Content, &record.Reasoning,
		&record.Error, &modelID, &record.ModelName, &stats, &record.CreatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Message{}, ErrMessageNotFound
		}
		return Message{}, fmt.Errorf("conversation: message scan: %w", err)
	}
	record.ModelID = modelID.String
	if stats != "" {
		var decoded Stats
		if json.Unmarshal([]byte(stats), &decoded) == nil {
			record.Stats = &decoded
		}
	}
	return record, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func truncate(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit])
}
