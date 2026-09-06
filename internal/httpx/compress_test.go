package httpx

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func compressed(t *testing.T, handler http.Handler, accept string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if accept != "" {
		request.Header.Set("Accept-Encoding", accept)
	}
	response := httptest.NewRecorder()
	Compress()(handler).ServeHTTP(response, request)
	return response
}

func TestCompressibleBodyIsGzipped(t *testing.T) {
	body := strings.Repeat(`{"key":"value"}`, 200)
	response := compressed(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}), "gzip")

	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if response.Header().Get("Content-Length") != "" {
		t.Error("Content-Length survived compression; it counts the wrong bytes now")
	}
	if !strings.Contains(response.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary does not name Accept-Encoding, so a cache can serve the wrong one")
	}

	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	round, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(round) != body {
		t.Error("the body did not survive the round trip")
	}
	if response.Body.Len() >= len(body) {
		t.Error("the compressed body is no smaller than the original")
	}
}

// The one that matters. A streamed answer through a compressor arrives when
// the buffer fills rather than when the model produced a word, and nothing in
// any log would say so.
func TestEventStreamIsNeverCompressed(t *testing.T) {
	response := compressed(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: delta\ndata: {\"text\":\"hello\"}\n\n")
		http.NewResponseController(w).Flush()
	}), "gzip")

	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("the event stream was encoded as %q", got)
	}
	if !strings.Contains(response.Body.String(), "event: delta") {
		t.Fatalf("the stream did not arrive verbatim: %q", response.Body.String())
	}
	if !response.Flushed {
		t.Error("the flush did not reach the client, which is how streaming stalls")
	}
}

// The SSE helper probes for flushability before it sets its own headers, so a
// response can be committed with no Content-Type at all. Anything unnamed has
// to be left alone.
func TestUnnamedContentTypeIsNotCompressed(t *testing.T) {
	response := compressed(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NewResponseController(w).Flush()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: late\n\n")
	}), "gzip")

	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("a response committed before its type was set was encoded as %q", got)
	}
}

func TestBodyIsLeftAloneWhenTheClientCannotDecode(t *testing.T) {
	response := compressed(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<p>hello</p>")
	}), "")

	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("encoded as %q for a client that never asked", got)
	}
	if response.Body.String() != "<p>hello</p>" {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestUncompressibleAndPartialResponsesAreUntouched(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		status      int
		encoding    string
	}{
		{"an image", "image/png", http.StatusOK, ""},
		{"a font", "font/woff2", http.StatusOK, ""},
		{"a byte range of the uncompressed file", "text/css", http.StatusPartialContent, ""},
		{"something already encoded", "text/html", http.StatusOK, "br"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := compressed(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.encoding != "" {
					w.Header().Set("Content-Encoding", test.encoding)
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, strings.Repeat("x", 500))
			}), "gzip")

			if got := response.Header().Get("Content-Encoding"); got != test.encoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, test.encoding)
			}
		})
	}
}

func TestAcceptEncodingIsReadCorrectly(t *testing.T) {
	for header, want := range map[string]bool{
		"gzip":                 true,
		"gzip, deflate, br":    true,
		"br;q=1.0, gzip;q=0.8": true,
		"*":                    true,
		"":                     false,
		"br":                   false,
		"deflate":              false,
		"gzip;q=0":             false,
		"identity, gzip;q=0.0": false,
		"br, gzip ; q=0":       false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
