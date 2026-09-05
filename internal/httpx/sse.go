package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SSE writes a Server-Sent Events response.
//
// The chat gateway streams over SSE rather than a websocket because the
// traffic only ever flows one way, it survives every proxy without an upgrade
// negotiation, and cancellation is nothing more than the client closing the
// connection — which cancels the request context, which cancels the upstream
// provider call.
type SSE struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// NewSSE commits the response headers and returns a writer for it. It fails
// only if the response cannot be flushed, which would make streaming
// pointless — every byte would sit in a buffer until the handler returned.
func NewSSE(w http.ResponseWriter) (*SSE, error) {
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("sse: response is not flushable: %w", err)
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Content-Type-Options", "nosniff")
	// nginx buffers proxied responses by default, which would hold a whole
	// answer back until it completed. This is the documented opt-out.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	stream := &SSE{w: w, rc: rc}
	return stream, stream.flush()
}

// Event writes one named event carrying a JSON payload.
func (s *SSE) Event(name string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sse: encode %s: %w", name, err)
	}
	return s.raw(name, body)
}

// Comment writes a `:` line. Used as a keepalive: it costs three bytes and
// keeps an idle proxy from closing a connection that is waiting on a slow
// first token.
func (s *SSE) Comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", strings.ReplaceAll(text, "\n", " ")); err != nil {
		return err
	}
	return s.flush()
}

func (s *SSE) raw(name string, data []byte) error {
	var b strings.Builder
	b.Grow(len(data) + len(name) + 16)
	if name != "" {
		b.WriteString("event: ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	// The payload is JSON, which never contains a raw newline, so a single
	// data line is always correct here.
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")

	if _, err := s.w.Write([]byte(b.String())); err != nil {
		return err
	}
	return s.flush()
}

func (s *SSE) flush() error {
	if err := s.rc.Flush(); err != nil {
		return fmt.Errorf("sse: flush: %w", err)
	}
	return nil
}
