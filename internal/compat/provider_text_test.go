package compat

import (
	"net/http"
	"strings"
	"testing"
)

// The provider's own words name what the operator presented under an API name:
// an upstream model, an endpoint. The caller is told a fixed sentence instead,
// and the words stay in the log.
func TestProviderRejectionDoesNotReachTheCaller(t *testing.T) {
	f := newFixture(t)
	const upstreamWords = "The model 'upstream-real-name-2024-08-06' does not exist"
	f.upstream.fail(http.StatusBadRequest, `{"error":{"message":"`+upstreamWords+`"}}`)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(f.model.ID))
	if w.Code < 400 {
		t.Fatalf("status = %d, want a failure: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "upstream-real-name") {
		t.Errorf("the provider's words reached the caller: %s", w.Body.String())
	}
}
