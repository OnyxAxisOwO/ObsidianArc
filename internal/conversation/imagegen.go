package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// ImageGeneration is a persistent record of an image produced in the Image Lab.
type ImageGeneration struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	AttachmentID  string `json:"attachment_id"`
	URL           string `json:"url"`
	ModelID       string `json:"model_id"`
	Prompt        string `json:"prompt"`
	RevisedPrompt string `json:"revised_prompt"`
	Size          string `json:"size"`
	Style         string `json:"style"`
	CreatedAt     int64  `json:"created_at"`
	// The picture's type, read from its attachment rather than stored a second
	// time. The download is named by it. Empty when the attachment has gone.
	Mime string `json:"mime,omitempty"`
}

type RecordImageGenerationInput struct {
	UserID        string
	AttachmentID  string
	ModelID       string
	Prompt        string
	RevisedPrompt string
	Size          string
	Style         string
}

// RecordImageGeneration writes a generated image row to the library.
//
// The prompt is cleaned the way a message is: PostgreSQL refuses a text value
// holding a NUL byte, and the handler validates the prompt without removing one,
// so an unclean prompt would fail here on Postgres only.
func (s *Store) RecordImageGeneration(ctx context.Context, in RecordImageGenerationInput) (ImageGeneration, error) {
	record := ImageGeneration{
		ID:            id.New(),
		UserID:        in.UserID,
		AttachmentID:  in.AttachmentID,
		URL:           "/api/attachments/" + in.AttachmentID,
		ModelID:       in.ModelID,
		Prompt:        text.Clean(in.Prompt),
		RevisedPrompt: text.Clean(in.RevisedPrompt),
		Size:          text.Clean(in.Size),
		Style:         text.Clean(in.Style),
		CreatedAt:     time.Now().UnixMilli(),
	}

	_, err := s.db.Exec(ctx,
		`INSERT INTO image_generations (id, user_id, attachment_id, model_id, prompt, revised_prompt, size, style, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.UserID, record.AttachmentID, record.ModelID, record.Prompt, record.RevisedPrompt, record.Size, record.Style, record.CreatedAt)
	if err != nil {
		return ImageGeneration{}, fmt.Errorf("conversation: record image generation: %w", err)
	}
	return record, nil
}

// ListImageGenerations returns the user's generated images, latest first. A page
// after the first is asked for with the created_at and id of the last row it
// returned. created_at alone would skip rows that share that millisecond with
// the cursor, so id orders those rows and the cursor is a position, not a time.
// An empty beforeID matches no tied row, which is the old timestamp-only cursor.
func (s *Store) ListImageGenerations(ctx context.Context, userID string, limit int, before int64, beforeID string) ([]ImageGeneration, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// The columns are qualified because attachments has id, user_id and
	// created_at too, so the join would leave the bare names ambiguous. LEFT, so a
	// generation whose picture row has gone still lists, with no mime.
	var (
		rows *sql.Rows
		err  error
	)
	if before > 0 {
		rows, err = s.db.Query(ctx,
			`SELECT g.id, g.user_id, g.attachment_id, g.model_id, g.prompt, g.revised_prompt, g.size, g.style, g.created_at, COALESCE(a.mime, '')
			 FROM image_generations g
			 LEFT JOIN attachments a ON a.id = g.attachment_id
			 WHERE g.user_id = ? AND (g.created_at < ? OR (g.created_at = ? AND g.id < ?))
			 ORDER BY g.created_at DESC, g.id DESC LIMIT ?`,
			userID, before, before, beforeID, limit)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT g.id, g.user_id, g.attachment_id, g.model_id, g.prompt, g.revised_prompt, g.size, g.style, g.created_at, COALESCE(a.mime, '')
			 FROM image_generations g
			 LEFT JOIN attachments a ON a.id = g.attachment_id
			 WHERE g.user_id = ?
			 ORDER BY g.created_at DESC, g.id DESC LIMIT ?`,
			userID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("conversation: list image generations: %w", err)
	}
	defer rows.Close()

	list := make([]ImageGeneration, 0)
	for rows.Next() {
		var g ImageGeneration
		if err := rows.Scan(&g.ID, &g.UserID, &g.AttachmentID, &g.ModelID, &g.Prompt, &g.RevisedPrompt, &g.Size, &g.Style, &g.CreatedAt, &g.Mime); err != nil {
			return nil, fmt.Errorf("conversation: scan image generation: %w", err)
		}
		g.URL = "/api/attachments/" + g.AttachmentID
		list = append(list, g)
	}
	return list, rows.Err()
}

// DeleteImageGeneration removes an image generation row and cleans up the underlying attachment.
//
// The attachment goes only while no message holds it. A picture that a chat
// already sent is part of that transcript, and deleting it from the library
// must not take the transcript's picture with it. Both deletes run in one
// transaction so the library row is never removed without its picture.
func (s *Store) DeleteImageGeneration(ctx context.Context, userID, id string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		var attachmentID string
		err := tx.QueryRow(ctx,
			`SELECT attachment_id FROM image_generations WHERE id = ? AND user_id = ?`,
			id, userID).Scan(&attachmentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("conversation: find image generation: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM image_generations WHERE id = ? AND user_id = ?`, id, userID); err != nil {
			return fmt.Errorf("conversation: delete image generation: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM attachments WHERE id = ? AND user_id = ? AND message_id IS NULL`,
			attachmentID, userID); err != nil {
			return fmt.Errorf("conversation: delete image attachment: %w", err)
		}
		return nil
	})
}
