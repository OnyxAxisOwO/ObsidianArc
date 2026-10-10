package chat

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// A body the client stops sending halfway is refused once its window runs out,
// and the answer reaches the client. net/http reads what is left of a short
// body before it writes the response, so lifting the deadline on that path
// left the read waiting for bytes that never came: no answer, and the
// connection and its goroutine held for as long as the client cared to wait.
func TestAStalledUploadIsAnsweredAndItsConnectionCloses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyRead := boundBodyReadFor(w, r, 300*time.Millisecond)
		var body struct {
			Name string `json:"name"`
		}
		err := httpx.DecodeJSON(w, r, &body, 1<<20)
		bodyRead()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Declares 100 bytes and sends the first eight, which are not a whole value.
	request := "POST / HTTP/1.1\r\nHost: test\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n" + `{"name":`
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("no answer to a stalled body: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// A body read to the end lifts its window, so work that runs past it — a
// picture being generated — is not mistaken for the client going away.
func TestACompleteUploadOutlastsItsWindow(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyRead := boundBodyReadFor(w, r, 200*time.Millisecond)
		var body struct {
			Name string `json:"name"`
		}
		err := httpx.DecodeJSON(w, r, &body, 1<<20)
		bodyRead()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case <-time.After(600 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want the work to finish past the window", resp.StatusCode)
	}
}
