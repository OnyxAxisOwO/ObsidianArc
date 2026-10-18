package web

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

const testShell = `<!doctype html><html><head><title>Arc</title></head>` +
	`<body><script type="module" src="/assets/index-a.js"></script></body></html>`

// The copies stand in for what the build writes; their bytes only have to be
// recognisable, because the server sends them as they are and never decodes.
func precompressedTree() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte(testShell)},
		"index.html.br":        {Data: []byte("shell-br")},
		"index.html.gz":        {Data: []byte("shell-gz")},
		"assets/index-a.js":    {Data: []byte(strings.Repeat("console.log(1);", 100))},
		"assets/index-a.js.br": {Data: []byte("script-br")},
		"assets/index-a.js.gz": {Data: []byte("script-gz")},
		"assets/index-a.css":   {Data: []byte(strings.Repeat("a{color:red}", 100))},
	}
}

// Wrapped in the compressor the way the server mounts it, because what
// matters is what leaves the whole chain: one Vary, and no second encoding.
func treeHandler(t *testing.T, tree fstest.MapFS, opts Options) http.Handler {
	t.Helper()
	h, err := handler(tree, opts)
	if err != nil {
		t.Fatal(err)
	}
	return httpx.Compress()(h)
}

func get(handler http.Handler, target, accept, inm string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}
	if inm != "" {
		req.Header.Set("If-None-Match", inm)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func varyCount(header http.Header) int {
	count := 0
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Accept-Encoding") {
				count++
			}
		}
	}
	return count
}

func TestAnAssetIsSentInTheBestCodingTheClientTakes(t *testing.T) {
	h := treeHandler(t, precompressedTree(), Options{})
	plain := strings.Repeat("console.log(1);", 100)
	for _, test := range []struct {
		accept, encoding, body string
	}{
		{"gzip, deflate, br", "br", "script-br"},
		{"br", "br", "script-br"},
		{"gzip", "gzip", "script-gz"},
		{"gzip, br;q=0", "gzip", "script-gz"},
		{"", "", plain},
		{"deflate", "", plain},
	} {
		t.Run("accept="+test.accept, func(t *testing.T) {
			rec := get(h, "/assets/index-a.js", test.accept, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Encoding"); got != test.encoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, test.encoding)
			}
			if rec.Body.String() != test.body {
				t.Errorf("body = %q, want the %q copy", rec.Body.String(), test.encoding)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
				t.Errorf("Content-Type = %q: a module script must be named as one", ct)
			}
			if n := varyCount(rec.Header()); n != 1 {
				t.Errorf("Vary names Accept-Encoding %d times, want once", n)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
				t.Errorf("Cache-Control = %q, want the fingerprinted-asset policy", cc)
			}
		})
	}
}

// Each coding has its own tag, so a revalidation confirms only the bytes the
// client actually holds.
func TestEachCodingRevalidatesOnItsOwnTag(t *testing.T) {
	h := treeHandler(t, precompressedTree(), Options{})
	tags := map[string]string{}
	for _, accept := range []string{"br", "gzip", ""} {
		rec := get(h, "/assets/index-a.js", accept, "")
		tag := rec.Header().Get("ETag")
		if tag == "" {
			t.Fatalf("accept=%q: no ETag", accept)
		}
		for other, seen := range tags {
			if seen == tag {
				t.Errorf("accept=%q and accept=%q share the tag %s", accept, other, tag)
			}
		}
		tags[accept] = tag

		again := get(h, "/assets/index-a.js", accept, tag)
		if again.Code != http.StatusNotModified {
			t.Errorf("accept=%q: revalidation with %s = %d, want 304", accept, tag, again.Code)
		}
		if again.Body.Len() != 0 {
			t.Errorf("accept=%q: a 304 carried a body", accept)
		}
	}
	if rec := get(h, "/assets/index-a.js", "gzip", tags["br"]); rec.Code != http.StatusOK || rec.Body.String() != "script-gz" {
		t.Errorf("the brotli copy's tag confirmed a gzip request: status %d", rec.Code)
	}
}

// A file the build did not compress — or a build that wrote no copies — is
// sent as before: as itself, gzipped on the way out by the compressor.
func TestAnAssetWithoutCopiesFallsBackToTheCompressor(t *testing.T) {
	h := treeHandler(t, precompressedTree(), Options{})
	css := strings.Repeat("a{color:red}", 100)

	rec := get(h, "/assets/index-a.css", "gzip, br", "")
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want the compressor's gzip", got)
	}
	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var round bytes.Buffer
	if _, err := round.ReadFrom(reader); err != nil {
		t.Fatal(err)
	}
	if round.String() != css {
		t.Error("the stylesheet did not survive the compressor")
	}
	if n := varyCount(rec.Header()); n != 1 {
		t.Errorf("Vary names Accept-Encoding %d times, want once", n)
	}
	tag := rec.Header().Get("ETag")
	if tag != `W/"index-a.css"` {
		t.Errorf("ETag = %q, want the weak file-name tag", tag)
	}
	if again := get(h, "/assets/index-a.css", "gzip", tag); again.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", again.Code)
	}

	if rec := get(h, "/assets/index-a.css", "", ""); rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != css {
		t.Errorf("a client that takes no coding got %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestTheShellIsSentPrecompressedUntilASubstitutionChangesIt(t *testing.T) {
	title := ""
	h := treeHandler(t, precompressedTree(), Options{Title: func() string { return title }})

	br := get(h, "/", "gzip, br", "")
	if br.Header().Get("Content-Encoding") != "br" || br.Body.String() != "shell-br" {
		t.Fatalf("Content-Encoding = %q, body %q: want the build's brotli shell", br.Header().Get("Content-Encoding"), br.Body.String())
	}
	if cc := br.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want the shell's no-cache", cc)
	}
	if ct := br.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if n := varyCount(br.Header()); n != 1 {
		t.Errorf("Vary names Accept-Encoding %d times, want once", n)
	}
	brTag := br.Header().Get("ETag")
	if again := get(h, "/", "gzip, br", brTag); again.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", again.Code)
	}

	gz := get(h, "/", "gzip", "")
	if gz.Header().Get("Content-Encoding") != "gzip" || gz.Body.String() != "shell-gz" {
		t.Fatalf("Content-Encoding = %q: want the build's gzip shell", gz.Header().Get("Content-Encoding"))
	}
	if gz.Header().Get("ETag") == brTag {
		t.Error("the gzip and brotli shells share a tag")
	}
	if plain := get(h, "/", "", ""); plain.Body.String() != testShell {
		t.Error("a client that takes no coding did not get the shell as built")
	}

	// The operator renames the site: the copies are of the old shell, so the
	// substituted one goes out instead, through the compressor.
	title = "Renamed"
	renamed := get(h, "/", "gzip, br", brTag)
	if renamed.Code != http.StatusOK {
		t.Fatalf("after a rename, status = %d, want 200", renamed.Code)
	}
	if got := renamed.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("after a rename, Content-Encoding = %q, want the compressor's gzip", got)
	}
	reader, err := gzip.NewReader(renamed.Body)
	if err != nil {
		t.Fatal(err)
	}
	var round bytes.Buffer
	if _, err := round.ReadFrom(reader); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(round.String(), "<title>Renamed</title>") {
		t.Errorf("the renamed shell does not carry the new title: %q", round.String())
	}
}

func TestAShellWithoutCopiesFallsBackToTheCompressor(t *testing.T) {
	tree := precompressedTree()
	delete(tree, "index.html.br")
	delete(tree, "index.html.gz")
	h := treeHandler(t, tree, Options{})

	rec := get(h, "/", "gzip, br", "")
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want the compressor's gzip", got)
	}
	if n := varyCount(rec.Header()); n != 1 {
		t.Errorf("Vary names Accept-Encoding %d times, want once", n)
	}
	if again := get(h, "/", "gzip, br", rec.Header().Get("ETag")); again.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", again.Code)
	}
}
