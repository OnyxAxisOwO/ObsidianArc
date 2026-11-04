package httpx

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The server stacks Logger outside Recover. A panic after a stream has begun
// must not have a JSON error written into the stream, and the access log must
// still record the request — which it only does when Recover is inside it.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logged
}

func TestPanicAfterTheStreamBeganWritesNoErrorIntoIt(t *testing.T) {
	logged := captureLog(t)
	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: first\n\n")
		panic("boom")
	}), Logger(), Recover())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/stream", nil))

	if body := response.Body.String(); body != "data: first\n\n" {
		t.Fatalf("the stream was followed by %q; an error body must not follow a started response", body)
	}
	if !strings.Contains(logged.String(), "msg=request") || !strings.Contains(logged.String(), "path=/api/stream") {
		t.Errorf("the access log has no line for the request that panicked:\n%s", logged.String())
	}
}

func TestPanicBeforeAnyWriteAnswersInternalErrorAndIsLogged(t *testing.T) {
	logged := captureLog(t)
	handler := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), Logger(), Recover())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/thing", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"internal"`) {
		t.Errorf("body = %q, want the internal error envelope", response.Body.String())
	}
	if !strings.Contains(logged.String(), "status=500") {
		t.Errorf("the access log did not record the 500:\n%s", logged.String())
	}
}
