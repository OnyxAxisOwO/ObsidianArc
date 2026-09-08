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
	// The row is still there; its bytes are not. Distinct from "no such
	// attachment" so the interface can say what happened rather than
	// pretending the picture was never sent.
	ErrAttachmentDiscarded = errors.New("conversation: this image is no longer held")
	ErrUnsupportedMedia    = errors.New("conversation: unsupported image type")
	ErrAttachmentTooLarge  = errors.New("conversation: image is too large")
	// Both are account-level: a signed-in caller that can repeat an upload
	// indefinitely is a way to fill the operator's disk.
	ErrTooManyPending      = errors.New("conversation: too many images are waiting to be sent")
	ErrAttachmentQuotaFull = errors.New("conversation: this account is holding as many images as it may")
)

const (
	// The default per-file ceiling, used when the operator has not set one.
	// The client downscales to roughly this before uploading (see the image
	// module in the frontend); the cap here is the backstop.
	MaxAttachmentBytes       = 6 * 1024 * 1024
	MaxAttachmentsPerMessage = 6
	// How long an uploaded image that was never attached to a message is
	// kept before the janitor removes it.
	//
	// Short, because it is the one window in which this server holds a
	// picture it has no use for: the composer uploads before the message is
	// sent, so an image chosen and then abandoned has nothing to discard it.
	// Nobody spends an hour composing one message.
	OrphanTTL = time.Hour

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
	// The operator's per-file ceiling in bytes. Zero means the default.
	MaxBytes int64
}

func (s *Store) Upload(ctx context.Context, in UploadInput) (Attachment, error) {
	if !allowedMedia[in.Mime] {
		return Attachment{}, ErrUnsupportedMedia
	}
	ceiling := in.MaxBytes
	if ceiling <= 0 {
		ceiling = MaxAttachmentBytes
	}
	if len(in.Data) == 0 || int64(len(in.Data)) > ceiling {
		return Attachment{}, ErrAttachmentTooLarge
	}

	record := Attachment{
		ID:     id.New(),
		Mime:   in.Mime,
		Width:  max(0, in.Width),
		Height: max(0, in.Height),
		Size:   len(in.Data),
	}

	// The limit check and insert must be one serialised operation. Without
	// the account-row lock, a burst of parallel uploads can all observe the
	// same old totals and each insert, bypassing both caps. A no-op UPDATE is
	// portable between SQLite and Postgres and takes the per-account write
	// lock until this transaction commits; unrelated users remain independent
	// on Postgres, while SQLite already serialises writers at database level.
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		locked, err := tx.Exec(ctx,
			`UPDATE users SET updated_at = updated_at WHERE id = ?`, in.UserID)
		if err != nil {
			return fmt.Errorf("conversation: lock attachment owner: %w", err)
		}
		if affected, rowsErr := locked.RowsAffected(); rowsErr == nil && affected == 0 {
			return fmt.Errorf("conversation: attachment owner does not exist")
		}

		// Checked before the insert rather than after, because the point is not
		// to store the row at all. Two counts in one statement: how many are
		// unattached, and how much the account holds altogether.
		var pending int
		var held int64
		if err := tx.QueryRow(ctx,
			`SELECT
			   COUNT(CASE WHEN message_id IS NULL THEN 1 END),
			   COALESCE(SUM(size), 0)
			 FROM attachments WHERE user_id = ?`, in.UserID).Scan(&pending, &held); err != nil {
			return fmt.Errorf("conversation: attachment usage: %w", err)
		}
		if pending >= MaxPendingAttachments {
			return ErrTooManyPending
		}
		if held+int64(len(in.Data)) > MaxAttachmentBytesPerUser {
			return ErrAttachmentQuotaFull
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO attachments (id, user_id, message_id, mime, width, height, size, data, created_at)
			 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
			record.ID, in.UserID, record.Mime, record.Width, record.Height, record.Size,
			in.Data, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("conversation: upload attachment: %w", err)
		}
		return nil
	})
	if err != nil {
		return Attachment{}, err
	}
	return record, nil
}

// Blob returns an attachment's bytes for the owning user. The ownership test
// is in the query, so serving someone else's image is not a check that could
// be skipped.
func (s *Store) Blob(ctx context.Context, userID, attachmentID string) (mime string, data []byte, err error) {
	var discardedAt int64
	err = s.db.QueryRow(ctx,
		`SELECT mime, data, discarded_at FROM attachments WHERE id = ? AND user_id = ?`,
		attachmentID, userID).Scan(&mime, &data, &discardedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return "", nil, ErrAttachmentNotFound
		}
		return "", nil, fmt.Errorf("conversation: read attachment: %w", err)
	}
	if discardedAt > 0 {
		return "", nil, ErrAttachmentDiscarded
	}
	return mime, data, nil
}

// Discard drops the bytes of every attachment in a conversation, keeping the
// rows as a record that something was sent.
//
// Called once a turn has been dispatched, which is the moment the bytes stop
// having a use: by then they have reached the provider, and the policy is
// that this server is not where a user's pictures live. The row keeps its
// mime, size and dimensions so the transcript can still show that an image
// was part of the message.
func (s *Store) Discard(ctx context.Context, conversationID string) (int64, error) {
	result, err := s.db.Exec(ctx,
		`UPDATE attachments SET data = ?, discarded_at = ?
		 WHERE discarded_at = 0
		   AND message_id IN (SELECT id FROM messages WHERE conversation_id = ? AND role = 'user')`,
		[]byte{}, time.Now().UnixMilli(), conversationID)
	if err != nil {
		return 0, fmt.Errorf("conversation: discard attachments: %w", err)
	}
	dropped, _ := result.RowsAffected()
	return dropped, nil
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
		 WHERE message_id IN (`+string(placeholders)+`) AND user_id = ? AND discarded_at = 0
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
