package compat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
)

// A body that says when somebody first reads it. Its length is not known until
// it has been read, which is what a chunked request looks like to the handler.
type watchedBody struct {
	r    io.Reader
	read atomic.Bool
}

func (b *watchedBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.r.Read(p)
}

// serveAsync sends one request through the mux from its own goroutine, as the
// server does, and reports when the handler has returned.
func (f *fixture) serveAsync(path string, body io.Reader) (*httptest.ResponseRecorder, chan struct{}) {
	request := httptest.NewRequest(http.MethodPost, path, body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+f.token)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.mux.ServeHTTP(recorder, request)
	}()
	return recorder, done
}

// A body of unknown length is read only once the process has a place for it,
// as the attachment endpoints read theirs. These endpoints take up to twelve
// megabytes of JSON, or forty of multipart, and nothing else bounded how many
// of them were in memory at once.
func TestAV1BodyIsNotReadWhileEveryDecodingPlaceIsTaken(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		body  func(t *testing.T, f *fixture) string
		reply string
	}{
		{
			name:  "chat completions",
			path:  "/v1/chat/completions",
			body:  func(_ *testing.T, f *fixture) string { return completionBody(f.model.ID) },
			reply: answer,
		},
		{
			name: "messages",
			path: "/v1/messages",
			body: func(_ *testing.T, f *fixture) string {
				return `{"model":"` + f.model.ID + `","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
			},
			reply: answer,
		},
		{
			name:  "responses",
			path:  "/v1/responses",
			body:  func(_ *testing.T, f *fixture) string { return responsesBody(f.model.ID) },
			reply: answer,
		},
		{
			name: "image generations",
			path: "/v1/images/generations",
			body: func(t *testing.T, f *fixture) string {
				imageModel(t, f, "painter", 0)
				return `{"model":"painter","prompt":"a sunset over mountains"}`
			},
			reply: `{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			body := c.body(t, f)
			f.upstream.reply(c.reply)

			f.handlers.Decoding = chat.NewDecodeGate(1)
			hold, err := f.handlers.Decoding.Acquire(context.Background(), "someone-else")
			if err != nil {
				t.Fatal(err)
			}

			read := &watchedBody{r: strings.NewReader(body)}
			recorder, done := f.serveAsync(c.path, read)
			time.Sleep(150 * time.Millisecond)
			if read.read.Load() {
				t.Fatal("the body was read while the only decoding place was taken")
			}

			hold()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the request never finished once the place was free")
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// A prompt is a few kilobytes and holds nothing, so a short request is not
// queued behind someone's upload while every place is taken.
func TestAPromptDoesNotWaitForADecodingPlace(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	f.handlers.Decoding = chat.NewDecodeGate(1)
	hold, err := f.handlers.Decoding.Acquire(context.Background(), "someone-else")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()

	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		answered <- f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(f.model.ID))
	}()
	select {
	case w := <-answered:
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a prompt waited for a decoding place")
	}
}
