package backup

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// An import that writes nothing is still a full read of the account's stored
// text, so a signed-in account could repeat one indefinitely. Each one is now
// held to a per-account rate: a short burst, then one every few seconds.
func TestAnAccountCannotRepeatImportsWithoutLimit(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)
	body := `{"obsidian_arc_export":1,"conversations":[]}`

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		httpx.Wrap(h.importDocument)(rec, asUser(
			httptest.NewRequest(http.MethodPost, "/api/account/import", strings.NewReader(body)), f.account))
		return rec
	}

	for i := 0; i < importBurst; i++ {
		if rec := do(); rec.Code != http.StatusOK {
			t.Fatalf("import %d of the burst: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("import past the burst: %d %s, want 429", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "import_rate_limited") {
		t.Errorf("refusal does not carry its code: %s", rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Errorf("refusal carries no Retry-After")
	}
}
