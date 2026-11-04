package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// The plain-http redirect through the assembled server. The middleware's own
// tests cannot see which peer the wiring treats as a proxy, which public
// address it names, or whether it sits in front of the page it protects.
func TestAPlainHTTPVisitorBehindCloudflareIsSentToTheSite(t *testing.T) {
	in := newInstance(t, func(cfg *config.Config) {
		cfg.Mail.PublicURL = "https://obsidian.example"
		cfg.TrustProxy = true
		cfg.TrustedProxies = []string{"10.0.0.1"}
		cfg.TrustCloudflare = true
	})

	get := func(target, peer, visitor string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.RemoteAddr = peer
		request.Header.Set("CF-Visitor", visitor)
		recorder := httptest.NewRecorder()
		in.handler.ServeHTTP(recorder, request)
		return recorder
	}

	plain := get("/chats?open=1", "10.0.0.1:5000", `{"scheme":"http"}`)
	if plain.Code != http.StatusPermanentRedirect || plain.Header().Get("Location") != "https://obsidian.example/chats?open=1" {
		t.Fatalf("a plain-http visitor: %d Location=%q", plain.Code, plain.Header().Get("Location"))
	}

	if secure := get("/chats?open=1", "10.0.0.1:5000", `{"scheme":"https"}`); secure.Code == http.StatusPermanentRedirect {
		t.Errorf("an https visitor was redirected: Location=%q", secure.Header().Get("Location"))
	}

	// A peer the operator did not name as a proxy cannot ask for the redirect.
	if stranger := get("/chats", "203.0.113.9:44321", `{"scheme":"http"}`); stranger.Code == http.StatusPermanentRedirect {
		t.Errorf("a stranger's report was believed: Location=%q", stranger.Header().Get("Location"))
	}

	// The health probe answers where it is probed, whatever the visitor's scheme.
	if health := get("/api/health", "10.0.0.1:5000", `{"scheme":"http"}`); health.Code == http.StatusPermanentRedirect {
		t.Errorf("the health probe was redirected: Location=%q", health.Header().Get("Location"))
	}
}
