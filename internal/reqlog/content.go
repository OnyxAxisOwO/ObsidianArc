package reqlog

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// Codex resends long transcripts; a single call must not fill the log queue
// with an unbounded copy. The details view makes this cutoff explicit.
const MaxContentBytes = 256 << 10

type contentReader struct {
	io.ReadCloser
	body      []byte
	truncated bool
}

func (r *contentReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	keep := min(n, MaxContentBytes-len(r.body))
	r.body = append(r.body, p[:keep]...)
	if keep < n {
		r.truncated = true
	}
	return n, err
}

// CaptureRequest observes the handler's reads instead of reading ahead: body
// limits, cancellation and streaming retain their existing behaviour.
func CaptureRequest(r *http.Request) {
	current, ok := r.Context().Value(noteKey).(*note)
	if !ok || r.Body == nil {
		return
	}
	reader := &contentReader{ReadCloser: r.Body}
	current.content = reader
	r.Body = reader
}

type Content struct {
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

func (s *Store) Content(ctx context.Context, id string) (Content, error) {
	var out Content
	err := s.db.QueryRow(ctx, `SELECT body, truncated FROM request_contents WHERE log_id = ?`, id).Scan(&out.Body, &out.Truncated)
	return out, err
}

func (s *Store) PruneContents(ctx context.Context, days int) (int64, error) {
	if days == 0 {
		result, err := s.db.Exec(ctx, `DELETE FROM request_contents`)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	result, err := s.db.Exec(ctx, `DELETE FROM request_contents WHERE log_id IN (SELECT id FROM request_log WHERE at < ?)`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func capturedContent(r *contentReader) *Content {
	if r == nil || len(r.body) == 0 {
		return nil
	}
	return &Content{Body: text.Clean(string(r.body)), Truncated: r.truncated}
}

// This counts retained UTF-8 body bytes, not database indexes or allocated
// disk pages. Deleting old bodies lets the database reuse their storage.
const MaxStoredContentBytes int64 = 2_000_000_000

func (s *Store) trimContents(ctx context.Context, tx *database.Tx) error {
	// Measure pre-cap records once, in small pages rather than loading all
	// previously recorded transcripts into memory at upgrade time.
	for {
		rows, err := tx.Query(ctx, `SELECT log_id, body FROM request_contents WHERE body_bytes < 0 LIMIT 100`)
		if err != nil {
			return err
		}
		type size struct {
			id    string
			bytes int
		}
		pending := []size{}
		for rows.Next() {
			var id, body string
			if err := rows.Scan(&id, &body); err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, size{id, len(body)})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			break
		}
		for _, entry := range pending {
			if _, err := tx.Exec(ctx, `UPDATE request_contents SET body_bytes = ? WHERE log_id = ?`, entry.bytes, entry.id); err != nil {
				return err
			}
		}
	}
	var total int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(body_bytes), 0) FROM request_contents`).Scan(&total); err != nil {
		return err
	}
	if total <= s.maxContentBytes {
		return nil
	}
	_, err := tx.Exec(ctx, `DELETE FROM request_contents WHERE log_id IN (
  SELECT log_id FROM (
   SELECT c.log_id, SUM(c.body_bytes) OVER (
    ORDER BY l.at DESC, l.id DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
   ) AS retained_bytes
   FROM request_contents c JOIN request_log l ON l.id = c.log_id
  ) sized WHERE retained_bytes > ?
 )`, s.maxContentBytes)
	return err
}
