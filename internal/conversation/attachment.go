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
	// An id in a send that cannot be claimed: expired and removed, already sent
	// with another message, discarded by retention, not this user's, or a library
	// picture. The send is refused as a whole, so nobody is left with a message
	// that quietly lacks the picture they attached.
	ErrAttachmentUnavailable = errors.New("conversation: an attached image is no longer available")
	// More distinct pictures than one message may hold. A cut would send the
	// message without the rest, which is the silent loss this file refuses
	// everywhere else.
	ErrTooManyAttachments = errors.New("conversation: too many images for one message")
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

// usage is what the two account-level caps are measured against: how many
// attachments are waiting for a message, and how many bytes the account is
// actually holding.
//
// Only bytes still held count. A discarded row keeps its size so the
// transcript can still say how big the picture was and the operator's storage
// page can still add it up, but the bytes are gone — and since the retention
// policy discards after the turn that sends them, counting those filled every
// account's allowance with images the database no longer stores.
func (s *Store) usage(ctx context.Context, q database.Queryer, userID string) (pending int, held int64, err error) {
	if q == nil {
		q = s.db
	}
	if err := q.QueryRow(ctx,
		`SELECT
		   COUNT(CASE WHEN message_id IS NULL AND id NOT IN (SELECT attachment_id FROM image_generations) THEN 1 END),
		   COALESCE(SUM(CASE WHEN discarded_at = 0 THEN size ELSE 0 END), 0)
		 FROM attachments WHERE user_id = ?`, userID).Scan(&pending, &held); err != nil {
		return 0, 0, fmt.Errorf("conversation: attachment usage: %w", err)
	}
	return pending, held, nil
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
		// to store the row at all.
		pending, held, err := s.usage(ctx, tx, in.UserID)
		if err != nil {
			return err
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
	dropped, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("conversation: discard attachments rows: %w", err)
	}
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

// distinctAttachmentIDs is the list a send actually claims. A picture named
// twice is claimed once: the second claim finds the row already taken and would
// refuse the whole send as a picture that is gone. The budget is counted after
// that, because a repeated id is not a second picture, and a list over the
// budget is refused rather than cut.
func distinctAttachmentIDs(attachmentIDs []string) ([]string, error) {
	seen := make(map[string]bool, len(attachmentIDs))
	distinct := make([]string, 0, len(attachmentIDs))
	for _, attachmentID := range attachmentIDs {
		if seen[attachmentID] {
			continue
		}
		seen[attachmentID] = true
		distinct = append(distinct, attachmentID)
	}
	if len(distinct) > MaxAttachmentsPerMessage {
		return nil, ErrTooManyAttachments
	}
	return distinct, nil
}

// linkAttachments claims previously uploaded images for a message. Only rows
// this user owns, that are not already attached, and whose bytes are still held
// can be claimed. An id that cannot be claimed fails the whole call with
// ErrAttachmentUnavailable, which the caller's transaction turns into nothing
// stored; the same error covers "gone", "someone else's", "already sent" and
// "discarded", so it reveals nothing about the others. A discarded picture must
// not be claimable: a detach lifts it off a message without restoring its bytes,
// and claiming it would store a message whose provider call silently omits the
// picture.
//
// A library picture is unattached too, but it belongs to the Image Lab
// library, not to a message. Claiming it would let the turn's dispatch wipe
// its bytes and deleting the library entry later would take it out of the
// transcript, so it is excluded here exactly as DeleteOrphans excludes it.
func (s *Store) linkAttachments(ctx context.Context, q database.Queryer, userID, messageID string, attachmentIDs []string) ([]Attachment, error) {
	ids, err := distinctAttachmentIDs(attachmentIDs)
	if err != nil {
		return nil, err
	}
	claimed := []Attachment{}
	for _, attachmentID := range ids {
		result, err := q.Exec(ctx,
			`UPDATE attachments SET message_id = ?
			 WHERE id = ? AND user_id = ? AND message_id IS NULL AND discarded_at = 0
			   AND id NOT IN (SELECT attachment_id FROM image_generations)`,
			messageID, attachmentID, userID)
		if err != nil {
			return nil, fmt.Errorf("conversation: link attachment: %w", err)
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return nil, fmt.Errorf("conversation: link attachment: %w", rowsErr)
		}
		if affected == 0 {
			return nil, ErrAttachmentUnavailable
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

// DetachPictures lifts the listed pictures off the messages that a truncation
// from messageID is about to delete. The delete cascades to attachment rows,
// so a picture the edited message re-sends would be gone before linkAttachments
// looks for it, and the edited message would silently lose it. Only pictures on
// the truncated tail are detached; one on an earlier message, or another
// user's, stays where it is.
func (s *Store) DetachPictures(ctx context.Context, q database.Queryer, userID, conversationID, messageID string, attachmentIDs []string) error {
	// Refused before any lock is taken: a send over the budget must not hold the
	// conversation for a detach that will never be followed by its claim.
	ids, err := distinctAttachmentIDs(attachmentIDs)
	if err != nil {
		return err
	}
	// A plain handle is routed through a transaction, as TruncateFrom does: the
	// lock below would otherwise commit on its own and the detach would run outside it.
	if _, ok := q.(*database.Tx); !ok {
		return s.db.Tx(ctx, func(tx *database.Tx) error {
			return s.DetachPictures(ctx, tx, userID, conversationID, messageID, attachmentIDs)
		})
	}
	// Attachment rows are written only once the conversation is locked, the same
	// order appendIn keeps (lock, then linkAttachments). Two requests in one
	// conversation that touch the same picture would otherwise each hold a row
	// the other needs, and PostgreSQL fails one of them.
	if err := lockConversation(ctx, q, userID, conversationID); err != nil {
		return err
	}
	for _, attachmentID := range ids {
		if _, err := q.Exec(ctx,
			`UPDATE attachments SET message_id = NULL
			 WHERE id = ? AND user_id = ?
			   AND message_id IN (
			     SELECT m.id FROM messages m
			     WHERE m.conversation_id = ? AND m.user_id = ?
			       AND m.seq >= (SELECT seq FROM messages WHERE id = ? AND conversation_id = ? AND user_id = ?))`,
			attachmentID, userID, conversationID, userID, messageID, conversationID, userID); err != nil {
			return fmt.Errorf("conversation: detach attachment: %w", err)
		}
	}
	return nil
}

// DeleteOrphans removes uploads that were never attached to a message: a
// picture chosen and then removed from the composer, or a browser closed
// mid-compose.
func (s *Store) DeleteOrphans(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UnixMilli()
	result, err := s.db.Exec(ctx,
		`DELETE FROM attachments
		 WHERE message_id IS NULL
		   AND id NOT IN (SELECT attachment_id FROM image_generations)
		   AND created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("conversation: prune attachments: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("conversation: prune attachments rows: %w", err)
	}
	return removed, nil
}
