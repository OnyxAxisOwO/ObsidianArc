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

// The lab is the only place an image model answers from.
//
// A turn used to branch into a picture on the same capability flag, which
// made one model two products depending on where it was used — and the
// transcript's half could only send a bare prompt at a fixed square. The
// refusal happens in Prepare, before the allowance is reserved and before
// anything is written, so the conversation the composer would have opened
// does not exist afterwards.
func TestAConversationRefusesAnImageModel(t *testing.T) {
	lab := newImageLab(t, 3)
	// A provider reply is queued so that a turn which wrongly reached the
	// upstream would succeed rather than fail for some unrelated reason.
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	recorder := lab.turn(t, `{"model_id":"`+lab.painter.ID+`","content":"a sunset"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "image lab") {
		t.Errorf("the refusal does not say where the model can be used: %s", recorder.Body.String())
	}

	conversations, err := lab.fixture.conversations.List(context.Background(), lab.fixture.account.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 0 {
		t.Errorf("a refused turn left %d conversations behind, want 0", len(conversations))
	}
	if records := lab.ledger(); len(records) != 0 {
		t.Errorf("a refused turn left %d ledger rows behind, want 0", len(records))
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
