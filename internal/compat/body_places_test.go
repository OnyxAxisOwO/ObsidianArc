package compat

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// watchedBody records when the handler first reads it. Its length is unknown to
// the handler until it has been read, as it is for a chunked request.
type watchedBody struct {
	r    io.Reader
	read atomic.Bool
}

func (b *watchedBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.r.Read(p)
}

// send starts a request through the mux from its own goroutine, as the server
// does, and reports when the handler has returned. The recorder may only be
// read once done is closed.
func (f *fixture) send(ctx context.Context, path, token, contentType string, body io.Reader) (*httptest.ResponseRecorder, chan struct{}) {
	request := httptest.NewRequest(http.MethodPost, path, body).WithContext(ctx)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.mux.ServeHTTP(recorder, request)
	}()
	return recorder, done
}

// slowRequest is a request whose body arrives in two parts. The first part is
// sent at once and the handler reads it; the rest is sent only by finish. In
// between, the handler is stuck in the middle of its body and holds whatever
// place it claimed.
type slowRequest struct {
	body     *watchedBody
	writer   *io.PipeWriter
	rest     string
	sent     chan struct{}
	recorder *httptest.ResponseRecorder
	done     chan struct{}
}

func (f *fixture) startSlow(t *testing.T, path, token, contentType, full string) *slowRequest {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.CloseWithError(io.ErrClosedPipe) })
	cut := len(full) / 2
	body := &watchedBody{r: reader}
	recorder, done := f.send(context.Background(), path, token, contentType, body)
	s := &slowRequest{
		body: body, writer: writer, rest: full[cut:], sent: make(chan struct{}),
		recorder: recorder, done: done,
	}
	go func() {
		defer close(s.sent)
		_, _ = writer.Write([]byte(full[:cut]))
	}()
	return s
}

// finish sends the rest of the body and returns the answer once the handler has
// returned. The first part is waited for before the rest is written, so the two
// cannot reach the handler out of order.
func (s *slowRequest) finish(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	waitDone(t, "the first part of a body to be read", s.sent)
	go func() {
		_, _ = s.writer.Write([]byte(s.rest))
		_ = s.writer.Close()
	}()
	waitDone(t, "a request whose body was finished", s.done)
	return s.recorder
}

// allRead reports whether every one of the requests has started reading its
// body, which is to say that each holds a place.
func allRead(slow []*slowRequest) bool {
	for _, s := range slow {
		if !s.body.read.Load() {
			return false
		}
	}
	return true
}

// placesState reports how many of the account's places are in use, and how many
// requests hold one or wait for one.
func placesState(f *fixture, account string) (inUse, requests int) {
	p := &f.handlers.bodies
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.holders[account]
	if entry == nil {
		return 0, 0
	}
	return len(entry.places), entry.refs
}

// accountsTracked is how many accounts the places map still has an entry for.
// It must be zero once every request has ended, or the map grows with every
// account that ever sent a body.
func accountsTracked(f *fixture) int {
	p := &f.handlers.bodies
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.holders)
}

// waitDone fails the test unless done closes within a few seconds.
func waitDone(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// waitUntil polls cond until it holds, and fails the test if it never does.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// addAccount makes another account in the fixture's group and returns its key.
func (f *fixture) addAccount(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	account, err := f.users.Create(ctx, nil, user.CreateInput{
		Username: name, PasswordHash: "x", GroupID: f.openGroup.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := f.keys.Issue(ctx, account.ID, name, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

const jsonContent = "application/json"

// pictureReply is what the upstream answers a picture request with.
const pictureReply = `{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`

// editForm is a picture edit sent as a multipart form, and the Content-Type that
// names its boundary.
func editForm(t *testing.T) (string, string) {
	t.Helper()
	picture, err := base64.StdEncoding.DecodeString(testPNG)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary("compat-bodies"); err != nil {
		t.Fatal(err)
	}
	_ = writer.WriteField("model", "painter")
	_ = writer.WriteField("prompt", "edit this image")
	part, err := writer.CreateFormFile("image", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(picture)
	_ = writer.Close()
	return body.String(), writer.FormDataContentType()
}

// bodyRequest is a /v1 endpoint that reads a request body: its path, a body it
// accepts with the Content-Type that body is sent under, and the answer the
// upstream gives it.
type bodyRequest struct {
	name  string
	path  string
	body  func(t *testing.T, f *fixture) (payload, contentType string)
	reply string
}

// bodyRequests is every /v1 endpoint that reads a body. The picture endpoints
// are in it because they read through the same places as chat, and a bound that
// skipped them would still let an account fill the process with pictures.
func bodyRequests() []bodyRequest {
	messagesBody := func(_ *testing.T, f *fixture) (string, string) {
		return `{"model":"` + f.model.ID + `","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, jsonContent
	}
	return []bodyRequest{
		{
			name: "chat completions", path: "/v1/chat/completions", reply: answer,
			body: func(_ *testing.T, f *fixture) (string, string) {
				return completionBody(f.model.ID), jsonContent
			},
		},
		{name: "messages", path: "/v1/messages", reply: answer, body: messagesBody},
		{name: "count tokens", path: "/v1/messages/count_tokens", reply: answer, body: messagesBody},
		{
			name: "responses", path: "/v1/responses", reply: answer,
			body: func(_ *testing.T, f *fixture) (string, string) {
				return responsesBody(f.model.ID), jsonContent
			},
		},
		{
			name: "image generations", path: "/v1/images/generations", reply: pictureReply,
			body: func(_ *testing.T, _ *fixture) (string, string) {
				return `{"model":"painter","prompt":"a sunset over mountains"}`, jsonContent
			},
		},
		{
			name: "image edits", path: "/v1/images/edits", reply: pictureReply,
			body: func(t *testing.T, _ *fixture) (string, string) {
				return editForm(t)
			},
		},
	}
}

// A body from one account waits for a place once the account holds all four, on
// every endpoint that reads one. The wait ends when one of the four finishes
// reading, and no earlier.
func TestAFifthBodyFromOneAccountWaitsForAPlace(t *testing.T) {
	for _, c := range bodyRequests() {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			imageModel(t, f, "painter", 0)
			f.upstream.reply(c.reply)
			payload, contentType := c.body(t, f)

			slow := make([]*slowRequest, maxBodyPlaces)
			for i := range slow {
				slow[i] = f.startSlow(t, c.path, f.token, contentType, payload)
			}
			waitUntil(t, "four bodies to be read", func() bool { return allRead(slow) })

			fifth := &watchedBody{r: strings.NewReader(payload)}
			fifthRec, fifthDone := f.send(context.Background(), c.path, f.token, contentType, fifth)
			time.Sleep(150 * time.Millisecond)
			if fifth.read.Load() {
				t.Fatal("the fifth body was read while its account held all four places")
			}
			select {
			case <-fifthDone:
				t.Fatalf("the fifth request was answered (%d) while its account held all four places", fifthRec.Code)
			default:
			}

			if w := slow[0].finish(t); w.Code != http.StatusOK {
				t.Fatalf("first body: status = %d: %s", w.Code, w.Body.String())
			}
			waitUntil(t, "the fifth body to be read once a place is free", func() bool { return fifth.read.Load() })
			waitDone(t, "the fifth request", fifthDone)
			if fifthRec.Code != http.StatusOK {
				t.Fatalf("fifth request: status = %d: %s", fifthRec.Code, fifthRec.Body.String())
			}
			for _, s := range slow[1:] {
				if w := s.finish(t); w.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", w.Code, w.Body.String())
				}
			}
			if inUse, requests := placesState(f, f.account.ID); inUse != 0 || requests != 0 {
				t.Fatalf("%d places in use and %d requests after every body, want none", inUse, requests)
			}
		})
	}
}

// A short prompt is bounded like any other body. Its size is no exemption, so an
// account cannot get past the bound by sending many small bodies at once.
func TestAShortPromptWaitsForItsAccountsPlaces(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	payload := completionBody(f.model.ID)

	slow := make([]*slowRequest, maxBodyPlaces)
	for i := range slow {
		slow[i] = f.startSlow(t, "/v1/chat/completions", f.token, jsonContent, payload)
	}
	waitUntil(t, "four bodies to be read", func() bool { return allRead(slow) })

	prompt, promptDone := f.send(context.Background(), "/v1/chat/completions", f.token, jsonContent, strings.NewReader(payload))
	time.Sleep(150 * time.Millisecond)
	select {
	case <-promptDone:
		t.Fatal("a prompt was answered while its account held all four places")
	default:
	}

	if w := slow[0].finish(t); w.Code != http.StatusOK {
		t.Fatalf("first body: status = %d: %s", w.Code, w.Body.String())
	}
	waitDone(t, "the prompt once a place is free", promptDone)
	if prompt.Code != http.StatusOK {
		t.Fatalf("prompt: status = %d: %s", prompt.Code, prompt.Body.String())
	}
	for _, s := range slow[1:] {
		if w := s.finish(t); w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
	}
}

// An account whose bodies trickle in holds only its own places. Another account
// is answered while all four of those bodies are still arriving.
func TestASlowBodyFromOneAccountDoesNotDelayAnother(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	otherToken := f.addAccount(t, "other")
	payload := completionBody(f.model.ID)

	slow := make([]*slowRequest, maxBodyPlaces)
	for i := range slow {
		slow[i] = f.startSlow(t, "/v1/chat/completions", f.token, jsonContent, payload)
	}
	waitUntil(t, "four bodies to be read", func() bool { return allRead(slow) })

	// Its length is unknown, so it has to claim a place before it is read.
	other := &watchedBody{r: strings.NewReader(payload)}
	otherRec, otherDone := f.send(context.Background(), "/v1/chat/completions", otherToken, jsonContent, other)
	waitDone(t, "another account's request while four of the first account's bodies trickle", otherDone)
	if otherRec.Code != http.StatusOK {
		t.Fatalf("other account: status = %d: %s", otherRec.Code, otherRec.Body.String())
	}

	for _, s := range slow {
		if w := s.finish(t); w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
	}
}

// Four accounts that each trickle one body fill a process-wide bound of four,
// and a fifth account would then wait behind all of them. Each account has places
// of its own, so the fifth is answered while the four bodies are still arriving.
func TestFourAccountsTrickleWithoutDelayingAFifth(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	payload := completionBody(f.model.ID)

	slow := make([]*slowRequest, 0, maxBodyPlaces)
	for _, name := range []string{"first", "second", "third", "fourth"} {
		token := f.addAccount(t, name)
		slow = append(slow, f.startSlow(t, "/v1/chat/completions", token, jsonContent, payload))
	}
	waitUntil(t, "four accounts' bodies to be read", func() bool { return allRead(slow) })

	fifthToken := f.addAccount(t, "fifth")
	fifth := &watchedBody{r: strings.NewReader(payload)}
	fifthRec, fifthDone := f.send(context.Background(), "/v1/chat/completions", fifthToken, jsonContent, fifth)
	waitDone(t, "a fifth account's request while four other accounts' bodies trickle", fifthDone)
	if fifthRec.Code != http.StatusOK {
		t.Fatalf("fifth account: status = %d: %s", fifthRec.Code, fifthRec.Body.String())
	}

	for _, s := range slow {
		if w := s.finish(t); w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
	}
}

// A place is given back once its body is parsed. A generation can run for
// minutes and must not hold one while it does, so the spend check, which comes
// just before the provider is called, finds none of the account's places held.
func TestNoPlaceIsHeldWhenTheProviderIsCalled(t *testing.T) {
	for _, c := range bodyRequests() {
		// Counting tokens answers without calling a provider, so there is no
		// call to check.
		if c.path == "/v1/messages/count_tokens" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			imageModel(t, f, "painter", 0)
			f.upstream.reply(c.reply)
			payload, contentType := c.body(t, f)

			var (
				reached         bool
				inUse, requests int
			)
			f.handlers.Guard = func(_ context.Context, account user.User, _ model.Model) (func(), error) {
				reached = true
				inUse, requests = placesState(f, account.ID)
				return func() {}, nil
			}

			rec, done := f.send(context.Background(), c.path, f.token, contentType, &watchedBody{r: strings.NewReader(payload)})
			waitDone(t, "the request", done)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if !reached {
				t.Fatal("the spend check was never reached, so the provider was never called")
			}
			if inUse != 0 || requests != 0 {
				t.Fatalf("%d places in use and %d requests when the provider was called, want none", inUse, requests)
			}
		})
	}
}

// A refused body gives its place back as well. Refusing more bodies than the
// account has places would wedge it if one of them were kept.
func TestARefusedBodyGivesItsPlaceBack(t *testing.T) {
	cases := []struct {
		name, path, payload string
	}{
		{"malformed JSON", "/v1/chat/completions", `{"model":`},
		// "not an image", base64-encoded: decodes, and then is refused as a picture.
		{"picture that is not one", "/v1/images/generations", `{"model":"painter","prompt":"x","image":"bm90IGFuIGltYWdl"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			imageModel(t, f, "painter", 0)
			f.upstream.reply(answer)

			for i := 0; i < 2*maxBodyPlaces; i++ {
				rec, done := f.send(context.Background(), c.path, f.token, jsonContent, strings.NewReader(c.payload))
				waitDone(t, "a refused body to be answered", done)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("refused body %d: status = %d, want 400: %s", i, rec.Code, rec.Body.String())
				}
			}

			payload := completionBody(f.model.ID)
			slow := make([]*slowRequest, maxBodyPlaces)
			for i := range slow {
				slow[i] = f.startSlow(t, "/v1/chat/completions", f.token, jsonContent, payload)
			}
			waitUntil(t, "every place to be free after the refused bodies", func() bool { return allRead(slow) })
			for _, s := range slow {
				if w := s.finish(t); w.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// A request queued for a place whose context is cancelled leaves the queue and
// holds nothing: its body is never read, and when everything has ended the
// bookkeeping is empty again. Over HTTP/1.1 a disconnect does not cancel an unread
// body's context, so the test cancels it directly.
func TestAWaitingRequestThatIsCancelledLeavesTheQueue(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	payload := completionBody(f.model.ID)

	slow := make([]*slowRequest, maxBodyPlaces)
	for i := range slow {
		slow[i] = f.startSlow(t, "/v1/chat/completions", f.token, jsonContent, payload)
	}
	waitUntil(t, "four bodies to be read", func() bool { return allRead(slow) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiter := &watchedBody{r: strings.NewReader(payload)}
	rec, done := f.send(ctx, "/v1/chat/completions", f.token, jsonContent, waiter)
	waitUntil(t, "the fifth request to queue behind the account's places", func() bool {
		_, requests := placesState(f, f.account.ID)
		return requests == maxBodyPlaces+1
	})

	cancel()
	waitDone(t, "the cancelled request", done)
	if waiter.read.Load() {
		t.Fatal("a cancelled request read its body")
	}
	if got := errorCode(t, rec); got != "cancelled" {
		t.Fatalf("code = %q, want cancelled: %s", got, rec.Body.String())
	}
	if inUse, requests := placesState(f, f.account.ID); inUse != maxBodyPlaces || requests != maxBodyPlaces {
		t.Fatalf("%d places in use and %d requests after the waiter left, want %d and %d",
			inUse, requests, maxBodyPlaces, maxBodyPlaces)
	}

	for _, s := range slow {
		if w := s.finish(t); w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
	}
	if inUse, requests := placesState(f, f.account.ID); inUse != 0 || requests != 0 {
		t.Fatalf("%d places in use and %d requests after every request ended, want none", inUse, requests)
	}
	if n := accountsTracked(f); n != 0 {
		t.Fatalf("%d accounts still tracked after every request ended, want none", n)
	}
}
