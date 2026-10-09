package chat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
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

func (l *imageLab) serve(path string, body io.Reader) (*httptest.ResponseRecorder, chan struct{}) {
	request := httptest.NewRequest(http.MethodPost, path, body)
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithUser(request.Context(), l.fixture.account))
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.mux.ServeHTTP(recorder, request)
	}()
	return recorder, done
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
