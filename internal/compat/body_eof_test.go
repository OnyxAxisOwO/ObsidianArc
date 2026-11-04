package compat

import (
	"io"
	"net/http/httptest"
	"testing"
)

// onceThenEOF hands its bytes over in one read and reports end of body only on
// the read after them, the way a chunked body arrives when its terminator is
// still on the wire. It records whether the reader got as far as the end.
type onceThenEOF struct {
	data    string
	reached bool
}

func (b *onceThenEOF) Read(p []byte) (int, error) {
	if b.data != "" {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	b.reached = true
	return 0, io.EOF
}

// A body that parses is read to its end before the request is served. The
// server lifts the body's read deadline and starts watching for the client to
// leave only once it has seen end of body; a body left short would keep the
// generation running after the client had gone.
func TestAParsedBodyIsReadToItsEnd(t *testing.T) {
	src := &onceThenEOF{data: `{"model":"m","stream":false}`}
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Body = io.NopCloser(src)
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/json")

	h := &Handlers{}
	var body struct {
		Model string `json:"model"`
	}
	if err := h.decode(httptest.NewRecorder(), r, caller{}, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Model != "m" {
		t.Fatalf("model = %q", body.Model)
	}
	if !src.reached {
		t.Error("the body was parsed without being read to its end")
	}
}
