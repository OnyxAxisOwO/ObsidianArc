package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The character limit has to survive the JSON that carries it. The encoder
// writes < > and & as six-byte escapes, so the largest body the rule allows,
// in that worst encoding, must reach validation rather than be refused by the
// HTTP cap, and one character more must get the announcement's own message.
func TestAdminAnnouncementBodyLimitHoldsInTheWorstEncoding(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	post := func(body string) *httptest.ResponseRecorder {
		return in.do(http.MethodPost, "/api/admin/announcements",
			map[string]any{"title": "Limit", "body": body}, founder)
	}

	if got := post(strings.Repeat("<", 32*1024)); got.Code != http.StatusCreated {
		t.Fatalf("a body of the full length: %d %s", got.Code, got.Body.String())
	}
	got := post(strings.Repeat("<", 32*1024+1))
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "That announcement is too long.") {
		t.Fatalf("one character over: %d %s", got.Code, got.Body.String())
	}
}
