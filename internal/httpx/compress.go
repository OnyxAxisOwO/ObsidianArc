package httpx

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Compress gzips a response when the client said it could take one.
//
// It is worth more than anything else this server does to what goes on the
// wire. The frontend is about 200 kB of JavaScript and CSS uncompressed and
// roughly a quarter of that compressed, and the deployment this project
// documents has nothing in front of it — no proxy, no CDN — so if the origin
// does not compress, nothing does.
//
// The decision is an allowlist rather than a denylist, because the one thing
// that must never be compressed is also the one that would fail silently. A
// streamed answer pushed through a compressor arrives when the compressor's
// buffer fills rather than when the model produced a word: the chat would sit
// still and then drop a paragraph, with nothing in any log to say why. So
// text/event-stream is not on the list, and neither is a response whose type
// the handler never stated — which is a belt-and-braces case now that the SSE
// helper names its type before committing anything, and was load-bearing when
// it did not.
func Compress() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Whether or not this response ends up compressed, it could have
			// been, so a cache between here and the reader must key on it.
			w.Header().Add("Vary", "Accept-Encoding")

			if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
				next.ServeHTTP(w, r)
				return
			}

			writer := &gzipWriter{ResponseWriter: w}
			defer writer.close()
			next.ServeHTTP(writer, r)
		})
	}
}

// Reused across requests: a gzip.Writer carries its window and hash tables,
// which is a few hundred kilobytes to allocate and throw away per response.
var gzipPool = sync.Pool{
	New: func() any { return gzip.NewWriter(io.Discard) },
}

// The types worth compressing, and the only ones that will be. Everything
// else — images, fonts, archives, the event stream — is either already
// compressed or must not be.
var compressible = map[string]bool{
	"text/html":                 true,
	"text/css":                  true,
	"text/plain":                true,
	"text/javascript":           true,
	"application/javascript":    true,
	"application/json":          true,
	"application/manifest+json": true,
	"image/svg+xml":             true,
}

type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
	on      bool
}

// WriteHeader is where the decision has to happen: the handler has set its
// Content-Type by now, and has not yet sent a byte.
func (w *gzipWriter) WriteHeader(status int) {
	w.decide(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipWriter) decide(status int) {
	if w.decided {
		return
	}
	w.decided = true

	// Only a plain, whole, uncompressed body of a type on the list. A 206 is
	// a byte range of the uncompressed representation and compressing it would
	// answer a different question than the one asked; a 304 has no body at all.
	if status != http.StatusOK {
		return
	}
	header := w.Header()
	if header.Get("Content-Encoding") != "" {
		return
	}
	if !compressible[mediaType(header.Get("Content-Type"))] {
		return
	}

	header.Set("Content-Encoding", "gzip")
	// The handler measured the body before it was compressed, so its count is
	// now wrong and the connection has to be framed by chunks instead.
	header.Del("Content-Length")

	w.gz = gzipPool.Get().(*gzip.Writer)
	w.gz.Reset(w.ResponseWriter)
	w.on = true
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.decided {
		w.WriteHeader(http.StatusOK)
	}
	if w.on {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush pushes the compressor's buffer out and then the writer beneath it, so
// a handler that flushes deliberately reaches the client.
//
// Every flush on a streamed answer goes through here. The stream itself is
// never compressed, so it was tempting to read this as a formality — it is
// not: a gzipWriter wraps the response of any client that sent
// Accept-Encoding, whatever type comes back, so this is the flush the chat
// and the API both depend on.
func (w *gzipWriter) Flush() {
	if w.on {
		_ = w.gz.Flush()
	}
	// Through the controller rather than a type assertion on the writer
	// directly beneath, because in the live chain that writer is the access
	// log's recorder — which offers Unwrap and not Flush. A bare assertion
	// finds no Flusher there and does nothing, so every flush on a streamed
	// answer became a silent no-op and the whole thing arrived in one lump
	// when Go's buffer filled. The controller walks the Unwrap chain to the
	// real writer, which is the only one that can actually flush.
	//
	// It only ever bit a client that sends Accept-Encoding, since nothing
	// else is wrapped at all: every browser and every agent's HTTP library
	// does, and plain curl does not, which is why hand-testing never saw it.
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap keeps http.ResponseController and Committed working through this
// layer. Flush above is found first, so the controller cannot reach past the
// compressor and leave its buffer behind.
func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipWriter) close() {
	if !w.on {
		return
	}
	_ = w.gz.Close()
	gzipPool.Put(w.gz)
	w.gz = nil
	w.on = false
}

// mediaType drops the parameters, so "text/html; charset=utf-8" is looked up
// as "text/html".
func mediaType(value string) string {
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

// acceptsGzip reads the header without weighing q-values: the only thing that
// matters is whether gzip is named and not explicitly refused with q=0.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		if name != "gzip" && name != "*" {
			continue
		}
		for _, parameter := range fields[1:] {
			parameter = strings.ToLower(strings.ReplaceAll(parameter, " ", ""))
			if parameter == "q=0" || strings.HasPrefix(parameter, "q=0.0") {
				return false
			}
		}
		return true
	}
	return false
}
