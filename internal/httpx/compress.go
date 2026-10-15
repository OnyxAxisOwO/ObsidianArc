package httpx

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
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
//
// The level is BestCompression, not the default. Go's default (level 6) runs
// its fast matcher and sends about 4% more than zlib's default on the
// frontend bundle, and these are the bytes a visitor waits for. Levels 7-9
// run the lazy matcher zlib uses. The cost is CPU per uncached response.
// The level is a constant, so NewWriterLevel cannot return its error here.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestCompression)
		return w
	},
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
	// A flush before any status sends the headers, so the compression decision
	// has to be made first: once they are on the wire, Content-Encoding can no
	// longer be added and a gzip body would reach the client unlabelled.
	if !w.decided {
		w.WriteHeader(http.StatusOK)
	}
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

// acceptsGzip reads the header the way HTTP defines it: the entry naming gzip
// outranks a wildcard wherever each appears, and only a quality of exactly
// zero refuses gzip.
//
// Both halves of that were wrong before, in opposite directions. Returning on
// the first entry that matched either name meant "*;q=0, gzip" was refused —
// but that header explicitly allows gzip, because the specific entry outranks
// the wildcard. And the quality was matched as a string prefix, so
// "gzip;q=0.001" read as a refusal: it is the faintest possible acceptance,
// not a rejection.
func acceptsGzip(header string) bool {
	wildcard, seen := 0.0, false
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		switch strings.ToLower(strings.TrimSpace(fields[0])) {
		case "gzip":
			return qualityOf(fields[1:]) > 0
		case "*":
			// Remembered rather than returned: a later gzip entry is more
			// specific and decides on its own.
			if !seen {
				wildcard, seen = qualityOf(fields[1:]), true
			}
		}
	}
	return seen && wildcard > 0
}

// qualityOf reads the q parameter. An absent parameter means 1, which is what
// makes a bare "gzip" acceptable, and a value that will not parse is treated
// as absent rather than as a refusal — a malformed header should not cost the
// client its compression.
func qualityOf(parameters []string) float64 {
	for _, parameter := range parameters {
		key, value, found := strings.Cut(strings.ToLower(strings.ReplaceAll(parameter, " ", "")), "=")
		if !found || key != "q" {
			continue
		}
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		}
	}
	return 1
}
