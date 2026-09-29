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
	handler := SecurityHeaders(false, nil, func() bool { return configured }, nil)(
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

// An in-page service a plugin runs — the self-hosted risk control one is the
// example — is a script, a telemetry connection and the images its puzzles
// are drawn from, with no frame of its own, which is the one difference from
// the Turnstile widening above. Its exception follows the same rule: granted
// only while the service is configured, gone the moment it is not.
func TestAPluginOriginIsAllowedOnlyWhileItsServiceIsConfigured(t *testing.T) {
	const origin = "https://risk.example.com"

	originNow := ""
	handler := SecurityHeaders(false, nil, nil, func() []string { return []string{originNow} })(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	policy := func() string {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		return recorder.Header().Get("Content-Security-Policy")
	}

	if strings.Contains(policy(), origin) {
		t.Errorf("the exception was granted with no service configured: %s", policy())
	}

	originNow = origin
	granted := policy()
	for _, directive := range []string{
		"script-src 'self' " + origin,
		"connect-src 'self' " + origin,
		"img-src 'self' data: blob: " + origin,
	} {
		if !strings.Contains(granted, directive) {
			t.Errorf("missing %q in %s", directive, granted)
		}
	}
	// Nothing of the service's renders in a frame, so the policy still has
	// no frame-src to hand out.
	if strings.Contains(granted, "frame-src") {
		t.Errorf("the risk origin opened a frame exception: %s", granted)
	}

	originNow = ""
	if strings.Contains(policy(), origin) {
		t.Errorf("unconfiguring the service left the exception behind: %s", policy())
	}
}

// Both challenges at once: each origin lands in each list exactly once, and
// the two exceptions neither overwrite nor duplicate each other.
func TestBothChallengeOriginsCompose(t *testing.T) {
	const turnstile = "https://challenges.cloudflare.com"
	const risk = "https://risk.example.com"

	handler := SecurityHeaders(false, nil,
		func() bool { return true },
		func() []string { return []string{risk} },
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	granted := recorder.Header().Get("Content-Security-Policy")

	for _, directive := range []string{
		"script-src 'self' " + turnstile + " " + risk,
		"connect-src 'self' " + turnstile + " " + risk,
		"img-src 'self' data: blob: " + risk,
		"frame-src " + turnstile,
	} {
		if !strings.Contains(granted, directive) {
			t.Errorf("missing %q in %s", directive, granted)
		}
	}
}

// An origin is one token of the header. A value carrying a separator would
// end the directive and start one of its own, so it is dropped rather than
// written — and two plugins' origins compose in the order given.
func TestPluginOriginsCannotInjectADirective(t *testing.T) {
	handler := SecurityHeaders(false, nil, nil, func() []string {
		return []string{"https://a.example.com", "https://b.example.com; script-src *", "", "https://c.example.com"}
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	granted := recorder.Header().Get("Content-Security-Policy")

	if !strings.Contains(granted, "script-src 'self' https://a.example.com https://c.example.com;") {
		t.Errorf("the clean origins did not compose: %s", granted)
	}
	if strings.Contains(granted, "b.example.com") || strings.Contains(granted, "script-src *") {
		t.Errorf("an origin with a separator reached the header: %s", granted)
	}
}
