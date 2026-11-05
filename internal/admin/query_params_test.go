package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An identifier from a body or a query is held to the alphabet one from the
// path is. Checking the length alone let twenty-six arbitrary characters
// through to the database as though they were an id.
func TestAnIdentifierOfTheRightLengthIsStillCheckedForItsAlphabet(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/audit?actor_id="+strings.Repeat("!", 26), nil)
	if _, err := (&Handlers{}).auditFilter(request); err == nil {
		t.Fatal("a 26-character actor id outside the alphabet was accepted")
	}
}
