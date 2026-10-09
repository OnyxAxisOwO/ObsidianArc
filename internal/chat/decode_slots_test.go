package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A body that says when somebody first reads it, and has no length: the
// shape of a chunked upload, and of any request whose size is not known
// before it is read.
type watchedBody struct {
	r    io.Reader
	read atomic.Bool
}

func (b *watchedBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.r.Read(p)
}

// start serves one request in its own goroutine, as the server does, as any
// account the test names, and reports when the handler has returned.
func (l *imageLab) start(ctx context.Context, account user.User, path string, body io.Reader) (*httptest.ResponseRecorder, chan struct{}) {
	request := httptest.NewRequest(http.MethodPost, path, body)
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithUser(ctx, account))
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.mux.ServeHTTP(recorder, request)
	}()
	return recorder, done
}

func (l *imageLab) serve(path string, body io.Reader) (*httptest.ResponseRecorder, chan struct{}) {
	return l.start(context.Background(), l.fixture.account, path, body)
}

// placesInUse counts the accounts the map still holds: those with a request
// holding their place or queued behind it. After every request has ended it
// must be zero, or the map is growing with the accounts that ever uploaded.
func (l *imageLab) placesInUse() int {
	s := &l.handlers.accountDecoding
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.holders)
}

// placeState reports how many requests hold or queue for the account's place,
// and whether the place is taken.
func (l *imageLab) placeState(account string) (refs int, held bool) {
	s := &l.handlers.accountDecoding
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.holders[account]
	if entry == nil {
		return 0, false
	}
	return entry.refs, len(entry.slot) == 1
}

func waitUntil(t *testing.T, what string, holds func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !holds() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// feed sends a request body from its own goroutine, so a request that is not
// reading yet cannot block the test while it writes.
func feed(w *io.PipeWriter, body string) {
	go func() {
		_, _ = io.WriteString(w, body)
		_ = w.Close()
	}()
}

func (l *imageLab) fillSlots() {
	for i := 0; i < cap(l.handlers.decoding); i++ {
		l.handlers.decoding <- struct{}{}
	}
}

func finishes(t *testing.T, done chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never finished", what)
	}
}

func uploadBody() string {
	return `{"mime":"image/png","width":1,"height":1,"data":"` + generatedPNG + `"}`
}

// The slot used to be claimed after the whole encoded body was in memory, so
// the bound was on the second copy and not on the first: any number of
// requests could be holding their entire upload while they queued.
func TestUploadClaimsItsSlotBeforeReadingTheBody(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fillSlots()

	body := &watchedBody{r: strings.NewReader(uploadBody())}
	recorder, done := lab.serve("/api/attachments", body)

	time.Sleep(150 * time.Millisecond)
	if body.read.Load() {
		t.Fatal("the body was read while every decoding slot was taken")
	}

	<-lab.handlers.decoding
	finishes(t, done, "the upload")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	if got := len(lab.handlers.decoding); got != cap(lab.handlers.decoding)-1 {
		t.Errorf("%d slots held after the upload, want the %d filled by the test", got, cap(lab.handlers.decoding)-1)
	}
}

func TestUploadHoldsItsSlotWhileTheBodyArrives(t *testing.T) {
	lab := newImageLab(t, 1)

	pipeReader, pipeWriter := io.Pipe()
	recorder, done := lab.serve("/api/attachments", pipeReader)

	deadline := time.Now().Add(5 * time.Second)
	for len(lab.handlers.decoding) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the upload never claimed a slot")
		}
		time.Sleep(time.Millisecond)
	}

	_, _ = io.WriteString(pipeWriter, uploadBody())
	_ = pipeWriter.Close()
	finishes(t, done, "the upload")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	if got := len(lab.handlers.decoding); got != 0 {
		t.Errorf("%d slots still held after the upload", got)
	}
}

// The image lab accepts pictures to work from, up to tens of megabytes of
// them, and used to decode every one with no bound on how many ran together.
func TestImageRequestWithPicturesSharesTheDecodingSlots(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)
	lab.fillSlots()

	body := &watchedBody{r: strings.NewReader(lab.askBody() + `}`)}
	recorder, done := lab.serve("/api/images/generate", body)

	time.Sleep(150 * time.Millisecond)
	if body.read.Load() {
		t.Fatal("a request of unknown size was read while every decoding slot was taken")
	}

	<-lab.handlers.decoding
	finishes(t, done, "the generation")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
}

// A prompt is not what the slots are for, and nobody typing one should queue
// behind somebody else's upload.
func TestPromptOnlyImageRequestDoesNotQueueForASlot(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)
	lab.fillSlots()

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- lab.generate(t, lab.askBody()+`}`) }()
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a prompt-only request waited for a decoding slot")
	}
}

// The provider call can take minutes. A slot held across it would stop every
// upload on the instance for that long.
func TestImageRequestGivesItsSlotBackBeforeCallingTheProvider(t *testing.T) {
	lab := newImageLab(t, 1)
	release := lab.fixture.upstream.replyHeld(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	recorder, done := lab.serve("/api/images/generate", &watchedBody{r: strings.NewReader(lab.askBody() + `}`)})

	deadline := time.Now().Add(5 * time.Second)
	for lab.fixture.upstream.lastCall().path == "" {
		if time.Now().After(deadline) {
			t.Fatal("the provider was never called")
		}
		time.Sleep(time.Millisecond)
	}
	if got := len(lab.handlers.decoding); got != 0 {
		t.Errorf("%d slots held during the provider call, want 0", got)
	}

	close(release)
	finishes(t, done, "the generation")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
}

// An account has one place at a time. Its second upload waits for the place
// without taking a global slot, and another account's upload is not queued
// behind it.
func TestSecondUploadFromOneAccountWaitsForItsPlace(t *testing.T) {
	lab := newImageLab(t, 1)

	firstBody, firstWriter := io.Pipe()
	firstRecorder, firstDone := lab.serve("/api/attachments", firstBody)
	waitUntil(t, "the first upload to claim a slot", func() bool { return len(lab.handlers.decoding) == 1 })

	// The second body is held back until the first upload has finished. Until
	// then a second upload that had taken a global slot would still be holding
	// it, and the count below would see it.
	secondBody, secondWriter := io.Pipe()
	secondRecorder, secondDone := lab.serve("/api/attachments", secondBody)
	time.Sleep(150 * time.Millisecond)
	if got := len(lab.handlers.decoding); got != 1 {
		t.Fatalf("%d global slots held, want 1: the second upload from the same account took one", got)
	}

	otherRecorder, otherDone := lab.start(context.Background(), lab.fixture.other, "/api/attachments", strings.NewReader(uploadBody()))
	finishes(t, otherDone, "the other account's upload")
	if otherRecorder.Code != http.StatusCreated {
		t.Fatalf("other account's upload: status = %d: %s", otherRecorder.Code, otherRecorder.Body)
	}

	feed(firstWriter, uploadBody())
	finishes(t, firstDone, "the first upload")
	feed(secondWriter, uploadBody())
	finishes(t, secondDone, "the second upload")
	if firstRecorder.Code != http.StatusCreated || secondRecorder.Code != http.StatusCreated {
		t.Fatalf("statuses %d and %d, want both %d", firstRecorder.Code, secondRecorder.Code, http.StatusCreated)
	}
	if got := len(lab.handlers.decoding); got != 0 {
		t.Errorf("%d global slots still held after every upload finished", got)
	}
	if got := lab.placesInUse(); got != 0 {
		t.Errorf("%d account places still in the map after every upload finished", got)
	}
}

// Four slow uploads from one account used to take every decoding slot, and
// nobody else's upload could start until they had all trickled in.
func TestOneAccountCannotHoldEveryDecodingSlot(t *testing.T) {
	lab := newImageLab(t, 1)

	writers := make([]*io.PipeWriter, cap(lab.handlers.decoding))
	recorders := make([]*httptest.ResponseRecorder, len(writers))
	dones := make([]chan struct{}, len(writers))
	for i := range writers {
		var body *io.PipeReader
		body, writers[i] = io.Pipe()
		recorders[i], dones[i] = lab.serve("/api/attachments", body)
	}
	waitUntil(t, "one upload to claim a slot", func() bool { return len(lab.handlers.decoding) > 0 })
	time.Sleep(150 * time.Millisecond)
	if got := len(lab.handlers.decoding); got != 1 {
		t.Fatalf("%d global slots held by one account, want 1", got)
	}

	otherRecorder, otherDone := lab.start(context.Background(), lab.fixture.other, "/api/attachments", strings.NewReader(uploadBody()))
	finishes(t, otherDone, "another account's upload")
	if otherRecorder.Code != http.StatusCreated {
		t.Fatalf("another account's upload: status = %d: %s", otherRecorder.Code, otherRecorder.Body)
	}

	// Which request holds the place is not known here, so every body is sent
	// from its own goroutine rather than in order.
	for _, writer := range writers {
		feed(writer, uploadBody())
	}
	for i, done := range dones {
		finishes(t, done, "an upload from the account")
		if recorders[i].Code != http.StatusCreated {
			t.Errorf("upload %d: status = %d: %s", i, recorders[i].Code, recorders[i].Body)
		}
	}
}

// A request queued behind its own account gives its place back when it ends,
// and the map forgets the account once nothing holds or waits for it.
func TestQueuedUploadThatEndsReleasesItsPlace(t *testing.T) {
	lab := newImageLab(t, 1)
	account := lab.fixture.account.ID

	firstBody, firstWriter := io.Pipe()
	firstRecorder, firstDone := lab.serve("/api/attachments", firstBody)
	waitUntil(t, "the first upload to take its place", func() bool {
		_, held := lab.placeState(account)
		return held
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, queuedDone := lab.start(ctx, lab.fixture.account, "/api/attachments", strings.NewReader(uploadBody()))
	waitUntil(t, "the second upload to queue behind the first", func() bool {
		refs, _ := lab.placeState(account)
		return refs == 2
	})
	cancel()
	finishes(t, queuedDone, "the queued upload after its request ended")

	feed(firstWriter, uploadBody())
	finishes(t, firstDone, "the first upload")
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("first upload: status = %d: %s", firstRecorder.Code, firstRecorder.Body)
	}
	if got := lab.placesInUse(); got != 0 {
		t.Fatalf("%d account places still in the map after the queued request ended", got)
	}

	recorder, done := lab.serve("/api/attachments", strings.NewReader(uploadBody()))
	finishes(t, done, "a later upload from the same account")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("later upload: status = %d: %s", recorder.Code, recorder.Body)
	}
	if got := lab.placesInUse(); got != 0 {
		t.Errorf("%d account places still in the map after the later upload", got)
	}
}

// A request that has its account's place and is then refused a global slot
// gives the place back as well.
func TestPlaceIsGivenBackWhenTheGlobalWaitEnds(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fillSlots()
	account := lab.fixture.account.ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, done := lab.start(ctx, lab.fixture.account, "/api/attachments", strings.NewReader(uploadBody()))
	waitUntil(t, "the upload to take its place", func() bool {
		_, held := lab.placeState(account)
		return held
	})
	cancel()
	finishes(t, done, "the upload after its request ended")
	if got := lab.placesInUse(); got != 0 {
		t.Fatalf("%d account places still held after the upload ended", got)
	}

	<-lab.handlers.decoding
	recorder, nextDone := lab.serve("/api/attachments", strings.NewReader(uploadBody()))
	finishes(t, nextDone, "the next upload from the same account")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("next upload: status = %d: %s", recorder.Code, recorder.Body)
	}
}

// A generation of unknown size takes the same place an upload does, so a
// generation cannot get around the account's one place by asking for pictures.
func TestGenerationOfUnknownSizeWaitsForItsAccountsPlace(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	firstBody, firstWriter := io.Pipe()
	_, firstDone := lab.serve("/api/attachments", firstBody)
	waitUntil(t, "the upload to claim a slot", func() bool { return len(lab.handlers.decoding) == 1 })

	generationBody, generationWriter := io.Pipe()
	recorder, done := lab.serve("/api/images/generate", generationBody)
	time.Sleep(150 * time.Millisecond)
	if got := len(lab.handlers.decoding); got != 1 {
		t.Fatalf("%d global slots held, want 1: the generation from the same account took one", got)
	}

	feed(firstWriter, uploadBody())
	finishes(t, firstDone, "the upload")
	feed(generationWriter, lab.askBody()+`}`)
	finishes(t, done, "the generation")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
}
