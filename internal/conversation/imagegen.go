package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
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
func (s *Store) RecordImageGeneration(ctx context.Context, in RecordImageGenerationInput) (ImageGeneration, error) {
	record := ImageGeneration{
		ID:            id.New(),
		UserID:        in.UserID,
		AttachmentID:  in.AttachmentID,
		URL:           "/api/attachments/" + in.AttachmentID,
		ModelID:       in.ModelID,
		Prompt:        in.Prompt,
		RevisedPrompt: in.RevisedPrompt,
		Size:          in.Size,
		Style:         in.Style,
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

// ListImageGenerations returns the user's generated images, latest first.
func (s *Store) ListImageGenerations(ctx context.Context, userID string, limit int, before int64) ([]ImageGeneration, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var (
		rows *sql.Rows
		err  error
	)
	if before > 0 {
		rows, err = s.db.Query(ctx,
			`SELECT id, user_id, attachment_id, model_id, prompt, revised_prompt, size, style, created_at
			 FROM image_generations
			 WHERE user_id = ? AND created_at < ?
			 ORDER BY created_at DESC LIMIT ?`,
			userID, before, limit)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT id, user_id, attachment_id, model_id, prompt, revised_prompt, size, style, created_at
			 FROM image_generations
			 WHERE user_id = ?
			 ORDER BY created_at DESC LIMIT ?`,
			userID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("conversation: list image generations: %w", err)
	}
	defer rows.Close()

	list := make([]ImageGeneration, 0)
	for rows.Next() {
		var g ImageGeneration
		if err := rows.Scan(&g.ID, &g.UserID, &g.AttachmentID, &g.ModelID, &g.Prompt, &g.RevisedPrompt, &g.Size, &g.Style, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("conversation: scan image generation: %w", err)
		}
		g.URL = "/api/attachments/" + g.AttachmentID
		list = append(list, g)
	}
	return list, rows.Err()
}

// DeleteImageGeneration removes an image generation row and cleans up the underlying attachment.
func (s *Store) DeleteImageGeneration(ctx context.Context, userID, id string) error {
	var attachmentID string
	err := s.db.QueryRow(ctx,
		`SELECT attachment_id FROM image_generations WHERE id = ? AND user_id = ?`,
		id, userID).Scan(&attachmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("conversation: find image generation: %w", err)
	}

	_, err = s.db.Exec(ctx, `DELETE FROM image_generations WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("conversation: delete image generation: %w", err)
	}

	if attachmentID != "" {
		_, _ = s.db.Exec(ctx, `DELETE FROM attachments WHERE id = ? AND user_id = ?`, attachmentID, userID)
	}
	return nil
}
