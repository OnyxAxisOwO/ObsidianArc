package reqlog

import (
	"context"
	"io"
	"net/http"
	"time"

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
