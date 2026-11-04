package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// The first paint is what a visitor downloads before the page can respond,
// and it has a budget in AGENTS.md. It is measured through the server's own
// chain, because the browser receives what that chain sends — the build's
// compressed copies, or the compressor's output where there are none — and a
// figure from a different encoder is not the number the budget is about.
//
// Measured twice, once per coding a browser can be sent. The budget is held
// on gzip: it is what every client takes, so it is the worst a visitor waits
// for. The brotli figure is what most browsers actually receive, and the docs
// quote both so neither is mistaken for the other.
func TestFirstPaintFitsItsBudgetOnTheWire(t *testing.T) {
	const budget = 208_000
	handler := httpx.Compress()(builtHandler(t))

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", index.Code)
	}
	names := regexp.MustCompile(`assets/(index-[^"'\s]+\.(?:js|css))`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(names) != 2 {
		t.Fatalf("index.html names %d first-paint assets, want 2 (script and stylesheet): %v", len(names), names)
	}

	sent := func(coding string) int {
		total := 0
		for _, match := range names {
			req := httptest.NewRequest(http.MethodGet, "/assets/"+match[1], nil)
			req.Header.Set("Accept-Encoding", coding)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", match[1], rec.Code)
			}
			// Without this the count below is of uncompressed bytes, which are
			// several times larger and would fail the budget for the wrong
			// reason — or, for br, of the gzip fallback, which would quote the
			// wrong figure as brotli's.
			if got := rec.Header().Get("Content-Encoding"); got != coding {
				t.Fatalf("%s is served with Content-Encoding %q, want %s", match[1], got, coding)
			}
			total += rec.Body.Len()
		}
		return total
	}
	gzipped, brotli := sent("gzip"), sent("br")
	t.Logf("first paint is %d bytes on the wire with gzip, %d with br", gzipped, brotli)

	// The docs quote the figures, and a reader compares them with the server.
	// The match must not start inside a longer number, or a stray 1247.38 kB
	// would stand in for 247.38 kB.
	for _, total := range []int{gzipped, brotli} {
		figure := fmt.Sprintf("%.2f kB", float64(total)/1000)
		quoted := regexp.MustCompile(`(?:^|[^\d.])` + regexp.QuoteMeta(figure))
		for _, doc := range []string{"README.md", "docs/ARCHITECTURE.md", "AGENTS.md"} {
			text, err := os.ReadFile(filepath.Join("..", "..", doc))
			if err != nil {
				t.Fatalf("read %s: %v", doc, err)
			}
			if !quoted.Match(text) {
				t.Errorf("%s does not quote the first paint as %s", doc, figure)
			}
		}
	}

	if brotli >= gzipped {
		t.Errorf("brotli sends %d bytes and gzip %d: the build's brotli copies are not doing their job", brotli, gzipped)
	}
	if gzipped > budget {
		t.Fatalf("first paint is %d bytes on the wire with gzip, budget %d", gzipped, budget)
	}
}
