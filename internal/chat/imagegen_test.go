package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// A one-pixel PNG, base64 as a provider would return it.
const generatedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

type imageLab struct {
	fixture *fixture
	mux     *http.ServeMux
	painter model.Model

	mu      sync.Mutex
	records []TurnRecord
}

func newImageLab(t *testing.T, requestWeight float64) *imageLab {
	t.Helper()
	f := newFixture(t)
	lab := &imageLab{fixture: f}

	painter, err := f.models.Create(context.Background(), model.CreateInput{
		ProviderID: f.model.ProviderID, ModelID: "painter",
		DisplayName: "Painter", Enabled: true,
		Capabilities: model.Capabilities{SupportsImageGen: true},
		Weights:      model.Weights{Request: requestWeight},
	})
	if err != nil {
		t.Fatal(err)
	}
	lab.painter = painter

	f.service.OnTurn = func(_ context.Context, record TurnRecord) {
		lab.mu.Lock()
		defer lab.mu.Unlock()
		lab.records = append(lab.records, record)
	}

	handlers := NewHandlers(f.service, f.conversations)
	lab.mux = http.NewServeMux()
	handlers.Routes(lab.mux)
	return lab
}

func (l *imageLab) generate(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/images/generate", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithUser(request.Context(), l.fixture.account))
	recorder := httptest.NewRecorder()
	l.mux.ServeHTTP(recorder, request)
	return recorder
}

func (l *imageLab) turn(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithUser(request.Context(), l.fixture.account))
	recorder := httptest.NewRecorder()
	l.mux.ServeHTTP(recorder, request)
	return recorder
}

func (l *imageLab) ledger() []TurnRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]TurnRecord{}, l.records...)
}

// Marking a model for the lab says nothing about conversations.
//
// It said a great deal for a while: the gateway refused a turn asked of any
// model with the capability, so an operator who wanted a model in the image
// lab lost it from the composer's picker — and most models that draw are
// perfectly good models to talk to. The lab is chosen by the endpoint the
// request arrives at, not by taking the model away from the transcript.
func TestAnImageModelIsStillAnOrdinaryChatModel(t *testing.T) {
	lab := newImageLab(t, 3)
	// A plain chat-completions body, not a stream: the painter is created
	// without streaming support, so the adapter asks for one response.
	lab.fixture.upstream.reply(`{"choices":[{"message":{"content":"a sentence, not a picture"}}]}`)

	recorder := lab.turn(t, `{"model_id":"`+lab.painter.ID+`","content":"hello"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "event: error") {
		t.Fatalf("the turn was refused: %s", recorder.Body.String())
	}

	// It answered through the chat endpoint: a conversation with this model
	// is a conversation, not a picture the composer could not steer.
	if path := lab.fixture.upstream.lastCall().path; !strings.HasSuffix(path, "/chat/completions") {
		t.Errorf("the turn went to %q, want the chat endpoint", path)
	}

	conversations, err := lab.fixture.conversations.List(context.Background(), lab.fixture.account.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 1 {
		t.Fatalf("the turn left %d conversations behind, want 1", len(conversations))
	}

	messages := lab.fixture.messages(t, conversations[0].ID)
	if len(messages) != 2 {
		t.Fatalf("the conversation holds %d messages, want the question and its answer", len(messages))
	}
	if messages[1].Content != "a sentence, not a picture" {
		t.Errorf("the answer is %q, want what the provider said", messages[1].Content)
	}
	if len(messages[1].Attachments) != 0 {
		t.Errorf("the answer carried %d pictures; a conversation does not generate them",
			len(messages[1].Attachments))
	}
}

// Generating a picture reserved an allowance, released it and settled
// nothing, so the panel was free and left nothing in the ledger while the
// same model used from the transcript did not.
func TestGeneratingAnImageIsChargedAndRecorded(t *testing.T) {
	lab := newImageLab(t, 3)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	recorder := lab.generate(t, `{"model_id":"`+lab.painter.ID+`","prompt":"a sunset"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	records := lab.ledger()
	if len(records) != 1 {
		t.Fatalf("wrote %d ledger records, want 1", len(records))
	}
	if records[0].Status != StatusOK {
		t.Errorf("status = %q, want ok", records[0].Status)
	}
	if records[0].Credits != 3 {
		t.Errorf("charged %v credits, want 3", records[0].Credits)
	}

	// And the picture came back as an attachment this server holds, not as a
	// link to the provider.
	var body struct {
		Images []struct {
			AttachmentID string `json:"attachment_id"`
			URL          string `json:"url"`
		} `json:"images"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Images) != 1 || body.Images[0].AttachmentID == "" {
		t.Fatalf("no attachment was stored: %+v", body.Images)
	}
	if !strings.HasPrefix(body.Images[0].URL, "/api/attachments/") {
		t.Errorf("url = %q, want one served by this server", body.Images[0].URL)
	}
}

// The count is on the wire, and the allowance was reserved once however many
// pictures were asked for.
// A picture to work from turns the call into an edit: a different endpoint,
// a multipart body, and the file carrying its own media type rather than the
// octet-stream a plain form file would have — which is what providers that
// sniff the upload refuse.
func TestGeneratingAnImageFromAReferencePictureUploadsIt(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	recorder := lab.generate(t, `{"model_id":"`+lab.painter.ID+
		`","prompt":"make it night","image":"`+generatedPNG+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	call := lab.fixture.upstream.lastCall()
	if !strings.HasSuffix(call.path, "/images/edits") {
		t.Errorf("path = %q, want the edits endpoint", call.path)
	}
	if !strings.HasPrefix(call.contentType, "multipart/form-data") {
		t.Errorf("content type = %q, want multipart", call.contentType)
	}

	form := readMultipart(t, call)
	if form.Value["prompt"] == nil || form.Value["prompt"][0] != "make it night" {
		t.Errorf("the prompt did not travel with the picture: %v", form.Value)
	}
	files := form.File["image"]
	if len(files) != 1 {
		t.Fatalf("image parts = %d, want 1", len(files))
	}
	if got := files[0].Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("the uploaded part is typed %q, want image/png", got)
	}
	if files[0].Filename != "image.png" {
		t.Errorf("filename = %q, want image.png — some providers read the extension", files[0].Filename)
	}
}

// The reference picture is sniffed rather than believed, the same way one
// arriving from a provider is: an upload endpoint that stores what it is told
// something is, is a way to store anything at all.
func TestAReferencePictureThatIsNotAnImageIsRefused(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	notAnImage := base64.StdEncoding.EncodeToString([]byte("<html>an error page</html>"))
	recorder := lab.generate(t, `{"model_id":"`+lab.painter.ID+
		`","prompt":"make it night","image":"`+notAnImage+`"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if call := lab.fixture.upstream.lastCall(); call.path != "" {
		t.Errorf("the provider was called anyway, at %q", call.path)
	}
}

func readMultipart(t *testing.T, call upstreamCall) *multipart.Form {
	t.Helper()
	_, params, err := mime.ParseMediaType(call.contentType)
	if err != nil {
		t.Fatalf("content type %q: %v", call.contentType, err)
	}
	form, err := multipart.NewReader(bytes.NewReader(call.body), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("read multipart: %v", err)
	}
	t.Cleanup(func() { _ = form.RemoveAll() })
	return form
}

func TestGeneratingImagesBoundsTheCount(t *testing.T) {
	lab := newImageLab(t, 0)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	recorder := lab.generate(t, `{"model_id":"`+lab.painter.ID+`","prompt":"many","n":500}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	asked := lab.fixture.upstream.lastRequest()
	if asked == nil {
		t.Fatal("the provider was never called")
	}
	count, _ := asked["n"].(float64)
	if int(count) != MaxImagesPerRequest {
		t.Errorf("asked the provider for %v images, want %d", count, MaxImagesPerRequest)
	}
}

// A chat model reaching this endpoint means somebody named it directly rather
// than picking it in the panel.
func TestGeneratingAnImageRefusesAChatModel(t *testing.T) {
	lab := newImageLab(t, 0)

	recorder := lab.generate(t, `{"model_id":"`+lab.fixture.model.ID+`","prompt":"a sunset"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if len(lab.ledger()) != 0 {
		t.Error("a refused model still wrote a ledger record")
	}
}

// The reported SSRF: a provider answers with a URL instead of bytes, and the
// server used to fetch it from inside its own network and hand the body back
// as an attachment.
func TestGeneratingAnImageWillNotFetchAProviderURLOnTheLoopback(t *testing.T) {
	lab := newImageLab(t, 0)

	var reached bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{"secret":"instance metadata"}`))
	}))
	defer internal.Close()

	lab.fixture.upstream.reply(`{"created":1,"data":[{"url":"` + internal.URL + `/internal-secret"}]}`)

	recorder := lab.generate(t, `{"model_id":"`+lab.painter.ID+`","prompt":"a sunset"}`)
	if reached {
		t.Error("the server fetched a loopback URL the provider named")
	}

	// Nothing was delivered, so this is a failure the panel can show rather
	// than an empty success it would render as a broken picture.
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	// And the provider's own URL is not handed to the browser either, which
	// would only move the fetch to the reader's machine.
	if strings.Contains(recorder.Body.String(), internal.URL) {
		t.Errorf("the provider's URL reached the client: %s", recorder.Body.String())
	}

	// The attempt is still on the ledger: it reached the provider.
	records := lab.ledger()
	if len(records) != 1 || records[0].Status != StatusOK {
		t.Errorf("ledger = %+v, want one ok record for a call the provider answered", records)
	}
}

// Somebody closing the panel mid-generation is not the provider failing.
// Recording it as one makes the dashboard's error rate a count of how often
// people change their mind, which is the distinction the turn path already
// draws in finishFailed.
func TestAbandonedImageGenerationIsRecordedAsAbortedNotFailed(t *testing.T) {
	lab := newImageLab(t, 1)
	// The provider sits on the request, so the cancellation below lands while
	// the generation is genuinely in flight rather than before it starts.
	release := lab.fixture.upstream.replyHeld(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)
	defer close(release)

	gone, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(150*time.Millisecond, cancel)

	request := httptest.NewRequest(http.MethodPost, "/api/images/generate",
		strings.NewReader(`{"model_id":"`+lab.painter.ID+`","prompt":"a sunset"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithUser(gone, lab.fixture.account))
	recorder := httptest.NewRecorder()
	lab.mux.ServeHTTP(recorder, request)

	records := lab.ledger()
	if len(records) != 1 {
		t.Fatalf("wrote %d ledger records, want 1", len(records))
	}
	if records[0].Status != StatusAborted {
		t.Errorf("status = %q, want %q", records[0].Status, StatusAborted)
	}
	if records[0].ErrorCode != "cancelled" {
		t.Errorf("error code = %q, want cancelled", records[0].ErrorCode)
	}
}
