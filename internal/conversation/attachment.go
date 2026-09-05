package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Attachments are uploaded before the message that carries them exists —
// a user picks a picture while still typing. They are stored owned by the
// user and unattached, then linked when the message is written.

var (
	ErrAttachmentNotFound = errors.New("conversation: no such attachment")
	ErrUnsupportedMedia   = errors.New("conversation: unsupported image type")
	ErrAttachmentTooLarge = errors.New("conversation: image is too large")
	// Both are account-level: a signed-in caller that can repeat an upload
	// indefinitely is a way to fill the operator's disk.
	ErrTooManyPending      = errors.New("conversation: too many images are waiting to be sent")
	ErrAttachmentQuotaFull = errors.New("conversation: this account is holding as many images as it may")
)

const (
	// The client downscales to roughly this before uploading (see the image
	// module in the frontend). The cap here is the backstop, and it is what
	// bounds how large a row — and therefore a re-sent turn — can get.
	MaxAttachmentBytes       = 6 * 1024 * 1024
	MaxAttachmentsPerMessage = 6
	// How long an uploaded image that was never attached to a message is
	// kept before the janitor removes it.
	OrphanTTL = 6 * time.Hour

	// What one account may be holding at once.
	//
	// A per-file ceiling alone bounds nothing: uploading is a write to the
	// database that any signed-in account can repeat, and six megabytes at a
	// time fills a disk quickly. These are the account-level bounds — how
	// many pictures may be waiting for a message, and how many bytes an
	// account may occupy in total.
	//
	// Generous for a person: nobody attaches twenty images to one unsent
	// message, and nobody's saved conversations hold a gigabyte of pictures.
	MaxPendingAttachments     = 24
	MaxAttachmentBytesPerUser = 512 * 1024 * 1024
)

// What both provider protocols accept, and nothing else. An image type the
// upstream would reject is better refused here, where the message names the
// problem.
var allowedMedia = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

func MediaAllowed(mime string) bool { return allowedMedia[mime] }

type UploadInput struct {
	UserID string
	Mime   string
	Width  int
	Height int
	Data   []byte
}

func (s *Store) Upload(ctx context.Context, in UploadInput) (Attachment, error) {
	if !allowedMedia[in.Mime] {
		return Attachment{}, ErrUnsupportedMedia
	}
	if len(in.Data) == 0 || len(in.Data) > MaxAttachmentBytes {
		return Attachment{}, ErrAttachmentTooLarge
	}

	// Checked before the insert rather than after, because the point is not
	// to store the row at all. Two counts in one statement: how many are
	// unattached, and how much the account holds altogether.
	var pending int
	var held int64
	err := s.db.QueryRow(ctx,
		`SELECT
		   COUNT(CASE WHEN message_id IS NULL THEN 1 END),
		   COALESCE(SUM(size), 0)
		 FROM attachments WHERE user_id = ?`, in.UserID).Scan(&pending, &held)
	if err != nil {
		return Attachment{}, fmt.Errorf("conversation: attachment usage: %w", err)
	}
	if pending >= MaxPendingAttachments {
		return Attachment{}, ErrTooManyPending
	}
	if held+int64(len(in.Data)) > MaxAttachmentBytesPerUser {
		return Attachment{}, ErrAttachmentQuotaFull
	}

	record := Attachment{
		ID:     id.New(),
		Mime:   in.Mime,
		Width:  max(0, in.Width),
		Height: max(0, in.Height),
		Size:   len(in.Data),
	}

	_, err = s.db.Exec(ctx,
		`INSERT INTO attachments (id, user_id, message_id, mime, width, height, size, data, created_at)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
		record.ID, in.UserID, record.Mime, record.Width, record.Height, record.Size,
		in.Data, time.Now().UnixMilli())
	if err != nil {
		return Attachment{}, fmt.Errorf("conversation: upload attachment: %w", err)
	}
	return record, nil
}

// Blob returns an attachment's bytes for the owning user. The ownership test
// is in the query, so serving someone else's image is not a check that could
// be skipped.
func (s *Store) Blob(ctx context.Context, userID, attachmentID string) (mime string, data []byte, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT mime, data FROM attachments WHERE id = ? AND user_id = ?`,
		attachmentID, userID).Scan(&mime, &data)
	if err != nil {
		if database.IsNotFound(err) {
			return "", nil, ErrAttachmentNotFound
		}
		return "", nil, fmt.Errorf("conversation: read attachment: %w", err)
	}
	return mime, data, nil
}

// LoadForMessages fetches the bytes of every image in a transcript, keyed by
// message. Used when building a request: the provider needs the pixels, not
// a reference.
func (s *Store) LoadForMessages(ctx context.Context, q database.Queryer, userID string, messageIDs []string) (map[string][]ImageData, error) {
	if q == nil {
		q = s.db
	}
	if len(messageIDs) == 0 {
		return map[string][]ImageData{}, nil
	}

	placeholders := make([]byte, 0, len(messageIDs)*2)
	args := make([]any, 0, len(messageIDs)+1)
	for i, messageID := range messageIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, messageID)
	}
	args = append(args, userID)

	rows, err := q.Query(ctx,
		`SELECT message_id, mime, data FROM attachments
		 WHERE message_id IN (`+string(placeholders)+`) AND user_id = ?
		 ORDER BY created_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("conversation: load attachment data: %w", err)
	}
	defer rows.Close()

	out := map[string][]ImageData{}
	for rows.Next() {
		var (
			messageID string
			image     ImageData
		)
		if err := rows.Scan(&messageID, &image.Mime, &image.Data); err != nil {
			return nil, fmt.Errorf("conversation: attachment data scan: %w", err)
		}
		out[messageID] = append(out[messageID], image)
	}
	return out, rows.Err()
}

// ImageData is an attachment's bytes, ready for an adapter.
type ImageData struct {
	Mime string
	Data []byte
}

// linkAttachments claims previously uploaded images for a message. Only rows
// this user owns and that are not already attached can be claimed, so an id
// guessed from somewhere else is silently skipped rather than stolen.
func (s *Store) linkAttachments(ctx context.Context, q database.Queryer, userID, messageID string, attachmentIDs []string) ([]Attachment, error) {
	claimed := []Attachment{}
	for i, attachmentID := range attachmentIDs {
		if i >= MaxAttachmentsPerMessage {
			break
		}
		result, err := q.Exec(ctx,
			`UPDATE attachments SET message_id = ? WHERE id = ? AND user_id = ? AND message_id IS NULL`,
			messageID, attachmentID, userID)
		if err != nil {
			return nil, fmt.Errorf("conversation: link attachment: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			continue
		}

		var record Attachment
		err = q.QueryRow(ctx,
			`SELECT id, mime, width, height, size FROM attachments WHERE id = ?`, attachmentID).
			Scan(&record.ID, &record.Mime, &record.Width, &record.Height, &record.Size)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("conversation: read linked attachment: %w", err)
		}
		claimed = append(claimed, record)
	}
	return claimed, nil
}

// DeleteOrphans removes uploads that were never attached to a message: a
// picture chosen and then removed from the composer, or a browser closed
// mid-compose.
func (s *Store) DeleteOrphans(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UnixMilli()
	result, err := s.db.Exec(ctx,
		`DELETE FROM attachments WHERE message_id IS NULL AND created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("conversation: prune attachments: %w", err)
	}
	removed, _ := result.RowsAffected()
	return removed, nil
}
