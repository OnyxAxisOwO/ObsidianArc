package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The challenge widget is a script, an iframe and a callback to somebody
// else's origin, and the policy this file exists to keep tight forbids all
// three. Widening it is therefore the difference between the feature working
// and the feature being three dead network requests — and the widening has
// to disappear again when the challenge does.
func TestTheChallengeOriginIsAllowedOnlyWhileAChallengeIsConfigured(t *testing.T) {
	const origin = "https://challenges.cloudflare.com"

	configured := false
	handler := SecurityHeaders(false, nil, func() bool { return configured })(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	policy := func() string {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		return recorder.Header().Get("Content-Security-Policy")
	}

	// Off: an instance with no challenge keeps exactly the policy it had
	// before this feature existed.
	if strings.Contains(policy(), origin) {
		t.Errorf("the exception was granted with no challenge configured: %s", policy())
	}

	configured = true
	granted := policy()
	// All three, because two of them working is a widget that draws and
	// never reports, which is worse than one that never draws.
	for _, directive := range []string{
		"script-src 'self' " + origin,
		"connect-src 'self' " + origin,
		"frame-src " + origin,
	} {
		if !strings.Contains(granted, directive) {
			t.Errorf("missing %q in %s", directive, granted)
		}
	}
	// And nothing else moved.
	if strings.Contains(granted, "unsafe-inline'; script") || strings.Contains(granted, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("the exception loosened something else: %s", granted)
	}

	configured = false
	if strings.Contains(policy(), origin) {
		t.Errorf("switching the challenge off left the exception behind: %s", policy())
	}
}
