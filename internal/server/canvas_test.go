package server

import (
	"net/http"
	"strings"
	"testing"
)

// Canvas through the whole server: the switch an administrator saves is the
// one /api/site reports, the frame's address exists only while it is on, and
// the frame's own policy replaces the application's rather than being added
// to it — while every other response keeps the application's untouched.
func TestCanvasSwitchGovernsTheSiteFlagAndTheFrame(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	siteFlag := func() bool {
		t.Helper()
		response := in.do(http.MethodGet, "/api/site", nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("site: %d %s", response.Code, response.Body.String())
		}
		return decode[struct {
			Canvas bool `json:"canvas_enabled"`
		}](t, response).Canvas
	}
	set := func(value string) {
		t.Helper()
		response := in.do(http.MethodPut, "/api/admin/settings",
			map[string]string{"chat.canvas_enabled": value}, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("save canvas=%s: %d %s", value, response.Code, response.Body.String())
		}
	}

	if siteFlag() {
		t.Error("a fresh instance reports Canvas on")
	}
	if got := in.do(http.MethodGet, "/canvas/frame", nil, nil).Code; got != http.StatusNotFound {
		t.Errorf("frame with Canvas off: %d, want 404", got)
	}

	set("true")
	if !siteFlag() {
		t.Error("/api/site did not report the saved switch")
	}
	frame := in.do(http.MethodGet, "/canvas/frame", nil, nil)
	if frame.Code != http.StatusOK {
		t.Fatalf("frame with Canvas on: %d %s", frame.Code, frame.Body.String())
	}
	policy := frame.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "sandbox allow-scripts") || strings.Contains(policy, "allow-same-origin") {
		t.Errorf("frame policy: %s", policy)
	}
	if strings.Contains(policy, "frame-ancestors 'none'") || strings.Contains(policy, "default-src 'self'") {
		t.Errorf("the application's policy leaked into the frame's: %s", policy)
	}
	if got := frame.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("frame X-Frame-Options = %q", got)
	}

	// Everything else is exactly as strict as it was.
	app := in.do(http.MethodGet, "/api/health", nil, nil).Header()
	if got := app.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("application X-Frame-Options = %q with Canvas on", got)
	}
	if appPolicy := app.Get("Content-Security-Policy"); strings.Contains(scriptSrc(appPolicy), "'unsafe-inline'") ||
		!strings.Contains(appPolicy, "frame-ancestors 'none'") {
		t.Errorf("application policy loosened with Canvas on: %s", appPolicy)
	}

	set("false")
	if siteFlag() {
		t.Error("/api/site still reports Canvas after switching it off")
	}
	if got := in.do(http.MethodGet, "/canvas/frame", nil, nil).Code; got != http.StatusNotFound {
		t.Errorf("frame after switching off: %d, want 404", got)
	}
}
