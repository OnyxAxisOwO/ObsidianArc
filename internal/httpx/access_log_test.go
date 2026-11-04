package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The access log is info for a successful API call and debug for a page or an
// asset, which the default level hides. /v1 is an API surface too: its
// successful calls used to be demoted with the assets, so the default log
// showed none of them.
func TestASuccessfulV1CallIsLoggedAtInfo(t *testing.T) {
	logged := captureLog(t)
	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), Logger())

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	out := logged.String()
	if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "path=/v1/chat/completions") {
		t.Errorf("a successful /v1 call was not logged at info:\n%s", out)
	}
}

func TestAnAssetIsNotLoggedAtInfo(t *testing.T) {
	logged := captureLog(t)
	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), Logger())

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	if out := logged.String(); strings.Contains(out, "path=/assets/app.js") {
		t.Errorf("a static asset reached the info log:\n%s", out)
	}
}

func TestIsAPIPathCoversTheAPIAndV1AndNothingElse(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/auth/me":  true,
		"/v1/models":    true,
		"/v1":           false,
		"/v10/models":   false,
		"/apix":         false,
		"/assets/a.js":  false,
		"/chat/some-id": false,
		"/":             false,
	} {
		if got := isAPIPath(path); got != want {
			t.Errorf("isAPIPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// The policy memo starts empty. A zero value would match a request that carries
// neither input and send an empty Content-Security-Policy on the first response.
func TestTheFirstResponseCarriesAPolicy(t *testing.T) {
	handler := SecurityHeaders(false, nil, nil, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := response.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("the first response carried an empty Content-Security-Policy")
	}
}
