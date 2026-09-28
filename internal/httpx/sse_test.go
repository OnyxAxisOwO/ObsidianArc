package httpx

import (
	"errors"
	"net/http"
	"os"
	"testing"
	"time"
)

// sseDeadlineWriter is a ResponseWriter that records every write deadline the
// SSE writer sets and can be told to fail its Write the way a stalled socket
// does. It implements SetWriteDeadline, so the ResponseController reaches it.
type sseDeadlineWriter struct {
	header    http.Header
	deadlines []time.Time
	writeErr  error
}

func newSSEDeadlineWriter() *sseDeadlineWriter {
	return &sseDeadlineWriter{header: http.Header{}}
}

func (d *sseDeadlineWriter) Header() http.Header { return d.header }
func (d *sseDeadlineWriter) WriteHeader(int)     {}
func (d *sseDeadlineWriter) Flush()              {}

func (d *sseDeadlineWriter) Write(p []byte) (int, error) {
	if d.writeErr != nil {
		return 0, d.writeErr
	}
	return len(p), nil
}

func (d *sseDeadlineWriter) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

// ssePlainWriter has no SetWriteDeadline: it stands in for the console's
// in-process recorder, where the deadline cannot be set but the frame must
// still go out.
type ssePlainWriter struct {
	header  http.Header
	written int
}

func (p *ssePlainWriter) Header() http.Header { return p.header }
func (p *ssePlainWriter) WriteHeader(int)     {}
func (p *ssePlainWriter) Flush()              {}
func (p *ssePlainWriter) Write(b []byte) (int, error) {
	p.written += len(b)
	return len(b), nil
}

// Every frame arms a deadline in the future before writing and clears it to
// the zero value afterwards, so the deadline only ever spans one frame — not
// the gap until the next token, nor the idle connection once it is pooled.
func TestSSEArmsThenClearsDeadlinePerFrame(t *testing.T) {
	rec := newSSEDeadlineWriter()
	before := time.Now()
	stream, err := NewSSE(rec)
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	if err := stream.Event("delta", map[string]string{"text": "hi"}); err != nil {
		t.Fatalf("Event: %v", err)
	}

	// Two frames (the header commit and one event), each recording arm+clear.
	if len(rec.deadlines) != 4 {
		t.Fatalf("want 4 deadline calls (arm+clear per frame), got %d", len(rec.deadlines))
	}
	for i, d := range rec.deadlines {
		if i%2 == 0 { // arm: a deadline in the future
			if !d.After(before) {
				t.Errorf("deadline %d: want a future arm, got %v", i, d)
			}
		} else if !d.IsZero() { // clear: the zero value disarms it
			t.Errorf("deadline %d: want a zero clear, got %v", i, d)
		}
	}
}

// A write that will not complete surfaces as an error, which the gateway's
// Emit contract turns into "stop the turn" and cancel the provider call.
func TestSSEWriteTimeoutSurfacesAsError(t *testing.T) {
	rec := newSSEDeadlineWriter()
	rec.writeErr = os.ErrDeadlineExceeded
	stream, err := NewSSE(rec)
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	err = stream.Event("delta", map[string]string{"text": "hi"})
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("want the deadline-exceeded write surfaced, got %v", err)
	}
}

// A writer that cannot take a deadline (the console recorder) still gets its
// frame: SetWriteDeadline is best-effort and its failure is ignored.
func TestSSEWithoutDeadlineSupportStillWrites(t *testing.T) {
	pw := &ssePlainWriter{header: http.Header{}}
	stream, err := NewSSE(pw)
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	if err := stream.Event("delta", map[string]string{"text": "hi"}); err != nil {
		t.Fatalf("Event: %v", err)
	}
	if pw.written == 0 {
		t.Fatal("frame was not written to a deadline-less writer")
	}
}
