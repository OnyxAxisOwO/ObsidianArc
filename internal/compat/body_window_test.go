package compat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// httpOutcome is what a request came back with. Its body is kept only for the
// message a failing test prints.
type httpOutcome struct {
	status int
	body   string
	err    error
}

// startRequest sends the request on its own goroutine, so the test can keep
// steering the server while it is in flight.
func startRequest(client *http.Client, req *http.Request) <-chan httpOutcome {
	done := make(chan httpOutcome, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			done <- httpOutcome{err: err}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		done <- httpOutcome{status: resp.StatusCode, body: string(body)}
	}()
	return done
}

// awaitOutcome fails the test unless the request comes back within a few seconds.
func awaitOutcome(t *testing.T, what string, done <-chan httpOutcome) httpOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	return httpOutcome{}
}

// sleepUntil returns once the clock reads at, or at once if it already does.
func sleepUntil(at time.Time) {
	time.Sleep(time.Until(at))
}

// serveWithReadTimeout puts the fixture's routes behind a real HTTP server whose
// whole-request read timeout is timeout, as cmd/server runs it, and gives bodies
// the same window. The window is set before the server starts, so the handlers
// read it without racing a write.
func (f *fixture) serveWithReadTimeout(t *testing.T, timeout time.Duration) *httptest.Server {
	t.Helper()
	f.handlers.bodyWindow = timeout
	ts := httptest.NewUnstartedServer(f.mux)
	ts.Config.ReadTimeout = timeout
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// A body that queues behind its account's four bodies gets a full window from
// the moment it is granted. The server's read deadline runs from when the headers
// were read, so without the re-arm a body that waited past it would be cut off
// before its first read and answered 400 as if its JSON were bad.
func TestAQueuedBodyGetsAFullWindowWhenItIsGranted(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	const window = 600 * time.Millisecond
	ts := f.serveWithReadTimeout(t, window)

	// Dialled first, so the server accepts it first and its deadline runs from
	// the accept. Its request goes out only once the four slow bodies hold their
	// places, and it then waits past that deadline.
	start := time.Now()
	early, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = early.Close() })

	sleepUntil(start.Add(window * 7 / 10))
	payload := completionBody(f.model.ID)
	cut := len(payload) / 2
	slowClient := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(slowClient.CloseIdleConnections)

	// Each slow body sends its first half at once and then holds its place until
	// the test sends the rest.
	writers := make([]*io.PipeWriter, maxBodyPlaces)
	slow := make([]<-chan httpOutcome, maxBodyPlaces)
	for i := range writers {
		reader, writer := io.Pipe()
		t.Cleanup(func() { _ = writer.CloseWithError(io.ErrClosedPipe) })
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", jsonContent)
		req.Header.Set("Authorization", "Bearer "+f.token)
		writers[i] = writer
		slow[i] = startRequest(slowClient, req)
		go func() { _, _ = writer.Write([]byte(payload[:cut])) }()
	}
	waitUntil(t, "four bodies to hold the account's places", func() bool {
		inUse, _ := placesState(f, f.account.ID)
		return inUse == maxBodyPlaces
	})

	// Sent at once and larger than the connection's read buffer, so the server
	// reads it from the connection after it is granted, not from what it already
	// buffered.
	big := `{"model":"` + f.model.ID + `","stream":false,"messages":[{"role":"user","content":"` +
		strings.Repeat("a", 256<<10) + `"}]}`
	fifthTransport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return early, nil },
	}
	t.Cleanup(fifthTransport.CloseIdleConnections)
	fifthReq, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	fifthReq.Header.Set("Content-Type", jsonContent)
	fifthReq.Header.Set("Authorization", "Bearer "+f.token)
	fifth := startRequest(&http.Client{Transport: fifthTransport}, fifthReq)
	waitUntil(t, "the fifth request to queue behind the account's places", func() bool {
		_, requests := placesState(f, f.account.ID)
		return requests == maxBodyPlaces+1
	})

	// The wait has to outlast the fifth's own deadline, which is window after the
	// accept, and end before the slow bodies' windows do.
	sleepUntil(start.Add(window + window*35/100))
	finish := func(i int) {
		_, _ = writers[i].Write([]byte(payload[cut:]))
		_ = writers[i].Close()
	}
	finish(0)

	if out := awaitOutcome(t, "the fifth request", fifth); out.err != nil || out.status != http.StatusOK {
		t.Fatalf("fifth body: status = %d, err = %v: %s", out.status, out.err, out.body)
	}
	for i := 1; i < maxBodyPlaces; i++ {
		finish(i)
	}
	for i, done := range slow {
		if out := awaitOutcome(t, fmt.Sprintf("slow body %d", i), done); out.err != nil || out.status != http.StatusOK {
			t.Fatalf("slow body %d: status = %d, err = %v: %s", i, out.status, out.err, out.body)
		}
	}
}

// The window covers the body and nothing after it. Once a body is read to its end
// the window is lifted, so the background read that watches for a disconnect
// cannot time out during a generation and cancel it. The Guard stands in for a
// provider call that runs well past the window.
func TestAGenerationOutlastingTheWindowIsNotCutOff(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	const window = 300 * time.Millisecond
	ts := f.serveWithReadTimeout(t, window)
	f.handlers.Guard = func(ctx context.Context, _ user.User, _ model.Model) (func(), error) {
		time.Sleep(3 * window)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return func() {}, nil
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(completionBody(f.model.ID)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", jsonContent)
	req.Header.Set("Authorization", "Bearer "+f.token)
	if out := awaitOutcome(t, "the generation", startRequest(&http.Client{}, req)); out.err != nil || out.status != http.StatusOK {
		t.Fatalf("status = %d, err = %v: %s", out.status, out.err, out.body)
	}
}

// A body that stops short is answered once its window runs out, and its
// connection closes after the answer. The window is not lifted when the body is
// given back unread, because net/http reads the rest of such a body before it
// writes the response, and that read must not wait for a client that has stopped.
func TestAStalledBodyIsAnsweredAndItsConnectionCloses(t *testing.T) {
	f := newFixture(t)
	ts := f.serveWithReadTimeout(t, 300*time.Millisecond)

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Declares 100 bytes, sends the first nine, which are not a whole value, and
	// then stops.
	request := "POST /v1/chat/completions HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer " + f.token +
		"\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n" + `{"model":`
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
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("connection still open after the answer: %v", err)
	}
}
