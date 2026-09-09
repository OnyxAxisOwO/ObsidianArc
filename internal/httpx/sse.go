package httpx

import (
	"bytes"
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
	// Asked without flushing, because a flush is what commits the response:
	// it sends the header block exactly as it stands, and at this point none
	// of the headers below are on it. Probing with a real flush is how the
	// stream went out with no Content-Type at all — every header set after
	// the probe was one the client never saw, including the one that says
	// this is an event stream.
	if !canFlush(w) {
		return nil, fmt.Errorf("sse: response is not flushable")
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

	stream := &SSE{w: w, rc: http.NewResponseController(w)}
	return stream, stream.flush()
}

// canFlush reports whether a flush will reach the client, without performing
// one. It walks the same Unwrap chain http.ResponseController does, because
// the controller can only answer the question by flushing — which is exactly
// the side effect NewSSE has to avoid until its headers are set.
func canFlush(w http.ResponseWriter) bool {
	for {
		switch w.(type) {
		case interface{ FlushError() error }, http.Flusher:
			return true
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = unwrapper.Unwrap()
	}
}

// Event writes one named event carrying a JSON payload.
func (s *SSE) Event(name string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sse: encode %s: %w", name, err)
	}
	return s.raw(name, body)
}

// Literal writes one data line exactly as given, with no JSON encoding.
//
// It exists for protocols whose stream is not entirely JSON — OpenAI's
// completion stream ends with a bare `data: [DONE]`, which a marshalled
// string would render as a quoted one.
func (s *SSE) Literal(data string) error { return s.raw("", []byte(data)) }

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
	// A bytes.Buffer rather than a strings.Builder because the frame is handed
	// to Write as bytes: building a string only to convert it back copied
	// every frame a second time, once per token of every answer.
	//
	// Function-local, not kept on the SSE: Comment is a keepalive, and the
	// moment one is sent from a ticker a shared buffer is a data race.
	var b bytes.Buffer
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

	if _, err := s.w.Write(b.Bytes()); err != nil {
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
