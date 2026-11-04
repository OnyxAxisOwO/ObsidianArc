package backup

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// stalledWriter is a client whose socket has stopped draining. A write blocks
// forever unless a write deadline has been armed, which is what the socket does
// without one: the kernel buffer is full, nothing returns, and the server's
// own timeouts do not cover response writes.
type stalledWriter struct {
	*httptest.ResponseRecorder
	writeDeadline time.Time
}

func (s *stalledWriter) SetWriteDeadline(t time.Time) error {
	s.writeDeadline = t
	return nil
}

func (s *stalledWriter) Write(p []byte) (int, error) {
	if s.writeDeadline.IsZero() {
		select {}
	}
	return 0, os.ErrDeadlineExceeded
}

// A reader that has stopped sending. Its reads block until a read deadline is
// armed on the connection, which is how the server bounds an upload.
type stalledReadWriter struct {
	*httptest.ResponseRecorder
	readDeadline time.Time
	// The deadline in force at the moment the body gave up. The handler clears
	// it once the body has been read, so the test cannot look it up afterwards.
	deadlineAtStall time.Time
}

func (s *stalledReadWriter) SetReadDeadline(t time.Time) error {
	s.readDeadline = t
	return nil
}

type stalledBody struct{ w *stalledReadWriter }

func (b stalledBody) Read(p []byte) (int, error) {
	if b.w.readDeadline.IsZero() {
		select {}
	}
	b.w.deadlineAtStall = b.w.readDeadline
	return 0, os.ErrDeadlineExceeded
}

func (b stalledBody) Close() error { return nil }

func slotsHeld(h *Handlers) (exports, imports int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exports, h.imports
}

// A reader that stops taking bytes must not hold one of the two process-wide
// export slots for as long as the socket stays open.
func TestAStalledExportReaderReleasesItsSlot(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)
	f.write(t, f.account, "About lamps", "what is a lamp")

	w := &stalledWriter{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		httpx.Wrap(h.export)(w, asUser(httptest.NewRequest(http.MethodGet, "/api/account/export", nil), f.account))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the export is still writing to a reader that stopped taking bytes, holding its slot")
	}
	if exports, _ := slotsHeld(h); exports != 0 {
		t.Fatalf("%d export slots still held after the stalled export returned", exports)
	}
}

// An upload whose body stops arriving must give its slot back well before the
// server's five-minute read timeout would.
func TestAStalledImportBodyReleasesItsSlotPromptly(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)

	w := &stalledReadWriter{ResponseRecorder: httptest.NewRecorder()}
	req := asUser(httptest.NewRequest(http.MethodPost, "/api/account/import", nil), f.account)
	req.Body = stalledBody{w: w}

	done := make(chan struct{})
	go func() {
		defer close(done)
		httpx.Wrap(h.importDocument)(w, req)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an import whose body stopped arriving still holds its slot")
	}
	if _, imports := slotsHeld(h); imports != 0 {
		t.Fatalf("%d import slots still held after the stalled body was abandoned", imports)
	}
	if w.deadlineAtStall.IsZero() || time.Until(w.deadlineAtStall) > 2*time.Minute {
		t.Fatalf("the body read deadline was %v away, want at most two minutes", time.Until(w.deadlineAtStall))
	}
}

// A client that sent a whole document and then nothing more. The decode returns
// once the document is complete, and what is left of the body is read by the
// server after the handler, so the read deadline must still be armed then.
type completeThenStalledBody struct {
	w    *stalledReadWriter
	data *strings.Reader
}

func (b completeThenStalledBody) Read(p []byte) (int, error) {
	if b.data.Len() > 0 {
		return b.data.Read(p)
	}
	if b.w.readDeadline.IsZero() {
		select {}
	}
	return 0, os.ErrDeadlineExceeded
}

func (b completeThenStalledBody) Close() error { return nil }

// Clearing the deadline once the decode returned left the server's own read of
// the rest of the body with no bound, where the five-minute ReadTimeout used to
// apply.
func TestAnImportLeavesItsBodyReadBounded(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)

	w := &stalledReadWriter{ResponseRecorder: httptest.NewRecorder()}
	req := asUser(httptest.NewRequest(http.MethodPost, "/api/account/import", nil), f.account)
	req.Body = completeThenStalledBody{w: w, data: strings.NewReader(`{"obsidian_arc_export":1,"conversations":[]}`)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		httpx.Wrap(h.importDocument)(w, req)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an import whose body stopped after the document never returned")
	}
	if w.readDeadline.IsZero() {
		t.Fatal("the import cleared its read deadline, leaving the server's read of the rest of the body unbounded")
	}
}
