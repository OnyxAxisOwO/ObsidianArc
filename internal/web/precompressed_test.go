package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// A tree laid out as the build lays dist out. The copies hold marker bytes
// rather than real compressed data, so each answer shows which file it came
// from.
func precompressedTree() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                            {Data: []byte("<!doctype html><title>t</title>")},
		"assets/index-abc.js":                   {Data: []byte("console.log('original')")},
		"precompressed/assets/index-abc.js.br":  {Data: []byte("brotli bytes")},
		"precompressed/assets/index-abc.js.gz":  {Data: []byte("gzip bytes")},
		"assets/only-raw-def.css":               {Data: []byte("body{}")},
		"precompressed/assets/index-abc.css.gz": {Data: []byte("unrelated")},
	}
}

func precompressedHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := handler(precompressedTree(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Through the compressor, as the server serves it, so a copy that is
	// already encoded is seen not to be encoded a second time.
	return httpx.Compress()(h)
}

func TestAnAssetIsServedFromTheCopyTheClientAccepts(t *testing.T) {
	h := precompressedHandler(t)
	cases := []struct {
		name, accept, encoding, body string
	}{
		{"brotli first", "gzip, deflate, br", "br", "brotli bytes"},
		{"gzip without brotli", "gzip", "gzip", "gzip bytes"},
		{"brotli refused", "br;q=0, gzip", "gzip", "gzip bytes"},
		{"identity", "", "", "console.log('original')"},
		{"identity when both are refused", "br;q=0, gzip;q=0", "", "console.log('original')"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil)
			if c.accept != "" {
				req.Header.Set("Accept-Encoding", c.accept)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("Content-Encoding"); got != c.encoding {
				t.Errorf("Content-Encoding = %q, want %q", got, c.encoding)
			}
			if got := rec.Body.String(); got != c.body {
				t.Errorf("body = %q, want %q", got, c.body)
			}
			if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
				t.Errorf("Content-Type = %q, want the original's", got)
			}
			if c.encoding != "" {
				if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(c.body)) {
					t.Errorf("Content-Length = %q, want %d", got, len(c.body))
				}
			}
			if got := rec.Header().Get("ETag"); got != `W/"index-abc.js"` {
				t.Errorf("ETag = %q", got)
			}
		})
	}
}

// A range names bytes of the original, so it is answered from the original;
// a revalidation and a HEAD behave as they did before there were copies.
func TestRangesRevalidationAndHeadOfAPrecompressedAsset(t *testing.T) {
	h := precompressedHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	req.Header.Set("Range", "bytes=0-6")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "console" || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("range = %d %q (encoding %q), want 206 of the original", rec.Code, rec.Body.String(), rec.Header().Get("Content-Encoding"))
	}

	req = httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	req.Header.Set("If-None-Match", `W/"index-abc.js"`)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("revalidation = %d with %d bytes, want 304 and none", rec.Code, rec.Body.Len())
	}

	req = httptest.NewRequest(http.MethodHead, "/assets/index-abc.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "br" {
		t.Errorf("HEAD = %d with %d bytes (encoding %q), want 200, none, br", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Encoding"))
	}
}

// An asset the build made no copy of is still compressed on the way out, and
// the copies are not reachable by their own path.
func TestAnAssetWithoutACopyAndTheCopiesOwnPaths(t *testing.T) {
	h := precompressedHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/only-raw-def.css", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "body{}" || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("uncopied asset = %d %q (encoding %q)", rec.Code, rec.Body.String(), rec.Header().Get("Content-Encoding"))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/precompressed/assets/index-abc.js.br", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a copy's own path = %d, want 404", rec.Code)
	}
}
