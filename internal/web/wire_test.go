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
// compressor, because the browser receives that encoder's output, and a
// figure from a different encoder is not the number the budget is about.
func TestFirstPaintFitsItsBudgetOnTheWire(t *testing.T) {
	const budget = 250_000
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

	total := 0
	for _, match := range names {
		req := httptest.NewRequest(http.MethodGet, "/assets/"+match[1], nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", match[1], rec.Code)
		}
		// Without this the count below is of uncompressed bytes, which are
		// several times larger and would fail the budget for the wrong reason.
		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("%s is served with Content-Encoding %q, want gzip", match[1], got)
		}
		total += rec.Body.Len()
	}
	t.Logf("first paint is %d bytes on the wire", total)

	// The docs quote the figure, and a reader compares it with the server. The
	// match must not start inside a longer number, or a stray 1247.38 kB would
	// stand in for 247.38 kB.
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

	if total > budget {
		t.Fatalf("first paint is %d bytes on the wire, budget %d", total, budget)
	}
}
