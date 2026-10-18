package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// With several malformed filters, the answer names the same one every time:
// the check walked a map, and which it named changed from request to request.
func TestUsageFilterNamesTheFirstMalformedParameterEveryTime(t *testing.T) {
	bad := strings.Repeat("!", 26)
	target := "/api/admin/usage?provider_id=" + bad + "&model_id=" + bad + "&group_id=" + bad + "&user_id=" + bad
	for range 50 {
		_, err := filterFrom(httptest.NewRequest(http.MethodGet, target, nil))
		response, ok := err.(*httpx.Error)
		if !ok {
			t.Fatalf("filterFrom = %v, want a 400", err)
		}
		if !strings.Contains(response.Error(), "user_id") {
			t.Fatalf("filterFrom named %q, want user_id first", response.Error())
		}
	}
}

// An identifier from a body or a query is held to the alphabet one from the
// path is. Checking the length alone let twenty-six arbitrary characters
// through to the database as though they were an id.
func TestAnIdentifierOfTheRightLengthIsStillCheckedForItsAlphabet(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/audit?actor_id="+strings.Repeat("!", 26), nil)
	if _, err := (&Handlers{}).auditFilter(request); err == nil {
		t.Fatal("a 26-character actor id outside the alphabet was accepted")
	}
}
