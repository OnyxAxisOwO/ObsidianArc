package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The one redirect this server makes on its own account. Each condition it
// checks gets a test of its own, because each is what stops the redirect from
// looping or from sending a working deployment somewhere it cannot answer.

const (
	publicHTTPS   = "https://obsidian.example"
	proxyPeer     = "10.0.0.1:5000"
	strangerPeer  = "203.0.113.9:44321"
	plainVisitor  = `{"scheme":"http"}`
	secureVisitor = `{"scheme":"https"}`
)

// trustedCloudflare is the deployment the redirect exists for: a proxy on
// 10.0.0.1 and the operator's claim that Cloudflare is in front of it.
func trustedCloudflare(t *testing.T) ProxyTrust {
	t.Helper()
	trust, err := NewProxyTrust(true, []string{"10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	return trust.WithCloudflare()
}

// redirectAnswer runs one request through PlainHTTPRedirect and reports what
// the client received, and whether the handler behind it was reached.
func redirectAnswer(t *testing.T, trust ProxyTrust, publicURL, method, target, peer string, headers map[string]string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	reached := false
	behind := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := PlainHTTPRedirect(trust, func() string { return publicURL })(behind)

	request := httptest.NewRequest(method, target, nil)
	request.RemoteAddr = peer
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder, reached
}

func TestAPlainHTTPPageLoadIsSentToThePublicAddress(t *testing.T) {
	recorder, reached := redirectAnswer(t, trustedCloudflare(t), publicHTTPS, http.MethodGet,
		"/chats/42?share=1", proxyPeer, map[string]string{"CF-Visitor": plainVisitor})

	if recorder.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want 308", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != publicHTTPS+"/chats/42?share=1" {
		t.Errorf("Location = %q, want the public address with the same path and query", got)
	}
	if reached {
		t.Error("the request went on to the handler behind the redirect")
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store so a remembered 308 cannot outlive a plain-http deployment", got)
	}
}

func TestTheRedirectKeepsThePathAsItArrived(t *testing.T) {
	recorder, _ := redirectAnswer(t, trustedCloudflare(t), publicHTTPS, http.MethodGet,
		"/a%2Fb?next=%2Fx%20y", proxyPeer, map[string]string{"CF-Visitor": plainVisitor})

	if got := recorder.Header().Get("Location"); got != publicHTTPS+"/a%2Fb?next=%2Fx%20y" {
		t.Errorf("Location = %q, want the escaped path and query carried over unchanged", got)
	}
}

// The report's scheme is compared without regard to case, and a report that
// is not JSON, or not a scheme at all, is no report: the request goes on.
func TestTheSchemeReportIsReadLeniently(t *testing.T) {
	claimed := trustedCloudflare(t)

	recorder, _ := redirectAnswer(t, claimed, publicHTTPS, http.MethodGet, "/", proxyPeer,
		map[string]string{"CF-Visitor": `{"scheme":"HTTP"}`})
	if recorder.Code != http.StatusPermanentRedirect {
		t.Errorf("an upper-case http report: status = %d, want 308", recorder.Code)
	}

	for _, report := range []string{"", secureVisitor, "not json", `["http"]`, `{"scheme":42}`} {
		recorder, reached := redirectAnswer(t, claimed, publicHTTPS, http.MethodGet, "/", proxyPeer,
			map[string]string{"CF-Visitor": report})
		if recorder.Code == http.StatusPermanentRedirect || !reached {
			t.Errorf("CF-Visitor %q: status = %d, reached = %v; want the request to go on", report, recorder.Code, reached)
		}
	}
}

// The header is believed only under the operator's claim. A deployment that
// has not said it is behind Cloudflare keeps the behaviour it had before.
func TestCloudflareVisitorIsIgnoredWithoutTheClaim(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	recorder, reached := redirectAnswer(t, trust, publicHTTPS, http.MethodGet, "/", proxyPeer,
		map[string]string{"CF-Visitor": plainVisitor})
	if recorder.Code == http.StatusPermanentRedirect || !reached {
		t.Errorf("without the claim: status = %d, reached = %v; want the request to go on", recorder.Code, reached)
	}
}

// Cloudflare's Flexible mode makes the origin leg http for every visitor, so
// X-Forwarded-Proto says http for visitors on https. A redirect keyed on it
// would loop, which is why it is never the reason for one.
func TestForwardedProtoNeverRedirects(t *testing.T) {
	unclaimed, err := NewProxyTrust(true, []string{"10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	claimed := trustedCloudflare(t)

	cases := []struct {
		name  string
		trust ProxyTrust
		hdrs  map[string]string
	}{
		{"claimed, forwarded http", claimed, map[string]string{"X-Forwarded-Proto": "http"}},
		{"claimed, flexible: forwarded http, visitor https", claimed,
			map[string]string{"X-Forwarded-Proto": "http", "CF-Visitor": secureVisitor}},
		{"unclaimed, forwarded http", unclaimed, map[string]string{"X-Forwarded-Proto": "http"}},
	}
	for _, tc := range cases {
		recorder, reached := redirectAnswer(t, tc.trust, publicHTTPS, http.MethodGet, "/", proxyPeer, tc.hdrs)
		if recorder.Code == http.StatusPermanentRedirect || !reached {
			t.Errorf("%s: status = %d, reached = %v; want the request to go on", tc.name, recorder.Code, reached)
		}
	}
}

// A connection that did not come from one of the operator's proxies cannot
// claim to have been reported by one, whatever it sends.
func TestAStrangerIsNeverRedirected(t *testing.T) {
	recorder, reached := redirectAnswer(t, trustedCloudflare(t), publicHTTPS, http.MethodGet, "/", strangerPeer,
		map[string]string{"CF-Visitor": plainVisitor, "X-Forwarded-Proto": "http"})
	if recorder.Code == http.StatusPermanentRedirect || !reached {
		t.Errorf("an untrusted peer: status = %d, reached = %v; want the request to go on", recorder.Code, reached)
	}
}

// The health probe reads its own status code and API clients read the shapes
// of their responses, so nothing under /api/ or /v1/ is ever redirected.
func TestAPIAndHealthPathsAreNeverRedirected(t *testing.T) {
	claimed := trustedCloudflare(t)
	for _, target := range []string{"/api/health", "/api/auth/me", "/api/conversations/42", "/v1/models", "/v1/models/a-model"} {
		recorder, reached := redirectAnswer(t, claimed, publicHTTPS, http.MethodGet, target, proxyPeer,
			map[string]string{"CF-Visitor": plainVisitor})
		if recorder.Code == http.StatusPermanentRedirect || !reached {
			t.Errorf("%s: status = %d, reached = %v; want the request to go on", target, recorder.Code, reached)
		}
	}
}

// A write is never redirected. Its body has already gone in the clear, and a
// redirect would only have the client send it again to the new address. HEAD
// is a read and is redirected like GET.
func TestOnlyReadsOfPagesAreRedirected(t *testing.T) {
	claimed := trustedCloudflare(t)
	headers := map[string]string{"CF-Visitor": plainVisitor}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		recorder, reached := redirectAnswer(t, claimed, publicHTTPS, method, "/", proxyPeer, headers)
		if recorder.Code == http.StatusPermanentRedirect || !reached {
			t.Errorf("%s: status = %d, reached = %v; want the request to go on", method, recorder.Code, reached)
		}
	}

	recorder, _ := redirectAnswer(t, claimed, publicHTTPS, http.MethodHead, "/", proxyPeer, headers)
	if recorder.Code != http.StatusPermanentRedirect {
		t.Errorf("HEAD: status = %d, want 308", recorder.Code)
	}
}

// With no https address configured there is nowhere correct to send anyone,
// so the request goes on. A plain-http address says the site is served over
// http, and is not a target for the redirect either.
func TestNoRedirectWithoutAnHTTPSPublicAddress(t *testing.T) {
	claimed := trustedCloudflare(t)
	headers := map[string]string{"CF-Visitor": plainVisitor}

	for _, publicURL := range []string{"", "   ", "http://obsidian.example", "obsidian.example"} {
		recorder, reached := redirectAnswer(t, claimed, publicURL, http.MethodGet, "/", proxyPeer, headers)
		if recorder.Code == http.StatusPermanentRedirect || !reached {
			t.Errorf("public address %q: status = %d, reached = %v; want the request to go on", publicURL, recorder.Code, reached)
		}
	}
}

// HSTS is set once, by SecurityHeaders, on every response outside development.
// A second header would give the browser two policies to choose between, and
// RFC 6797 section 8.1 says it takes the first. includeSubDomains or preload
// would bind names under this domain that may still be served over http.
func TestHSTSIsOneHeaderWithoutSubdomainsOrPreload(t *testing.T) {
	handler := SecurityHeaders(false, nil, nil, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	values := recorder.Header().Values("Strict-Transport-Security")
	if len(values) != 1 || values[0] != "max-age=31536000" {
		t.Errorf("Strict-Transport-Security = %q, want one header: max-age=31536000 with no includeSubDomains or preload", values)
	}
}
