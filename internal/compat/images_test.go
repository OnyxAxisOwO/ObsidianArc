package compat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// imageModel adds a model that actually generates images. The fixture's own
// model is a chat model, and the endpoint refuses those now.
func imageModel(t *testing.T, fix *fixture, name string, requestWeight float64) model.Model {
	t.Helper()
	record, err := fix.models.Create(context.Background(), model.CreateInput{
		ProviderID: fix.model.ProviderID, ModelID: name,
		DisplayName: "Mock Painter", Enabled: true,
		Capabilities: model.Capabilities{SupportsImageGen: true},
		Weights:      model.Weights{Request: requestWeight},
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestImagesGenerations(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{
		"created": 1700000000,
		"data": [
			{"b64_json": "aGVsbG8=", "revised_prompt": "revised prompt"}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{
		"model": "painter",
		"prompt": "a sunset over mountains"
	}`))
	req.Header.Set("Authorization", "Bearer "+fix.token)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	fix.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var resp openAIImageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("got %d images, want 1", len(resp.Data))
	}
	if resp.Data[0].B64JSON != "aGVsbG8=" {
		t.Errorf("got b64_json %q, want aGVsbG8=", resp.Data[0].B64JSON)
	}
	if resp.Data[0].RevisedPrompt != "revised prompt" {
		t.Errorf("got revised prompt %q, want 'revised prompt'", resp.Data[0].RevisedPrompt)
	}
}

// The endpoint used to reserve an allowance, release it and settle nothing,
// so pictures were free and left no trace in the ledger.
func TestImagesGenerationsIsRecorded(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 2)
	fix.upstream.reply(`{"created": 1, "data": [{"b64_json": "aGVsbG8="}, {"b64_json": "aGVsbG8="}]}`)

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model": "painter", "prompt": "two of them", "n": 2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	fix.mu.Lock()
	defer fix.mu.Unlock()
	if len(fix.records) != 1 {
		t.Fatalf("wrote %d ledger records, want 1", len(fix.records))
	}
	// Two pictures at a request weight of two.
	if got := fix.records[0].Credits; got != 4 {
		t.Errorf("charged %v credits, want 4", got)
	}
}

// A count is on the wire, and one call may not spend an unbounded number of
// times the allowance it reserved once.
func TestImagesGenerationsBoundsCount(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created": 1, "data": [{"b64_json": "aGVsbG8="}]}`)

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model": "painter", "prompt": "many", "n": 500}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	asked, _ := fix.upstream.received()["n"].(float64)
	if int(asked) != chat.MaxImagesPerRequest {
		t.Errorf("asked the provider for %v images, want %d", asked, chat.MaxImagesPerRequest)
	}
}

// A chat model reaching this endpoint means somebody named it directly.
func TestImagesGenerationsRefusesAChatModel(t *testing.T) {
	fix := newFixture(t)

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model": "upstream-real-name", "prompt": "a sunset"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestImagesGenerationsRequiresPrompt(t *testing.T) {
	fix := newFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{
		"model": "model-id",
		"prompt": ""
	}`))
	req.Header.Set("Authorization", "Bearer "+fix.token)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	fix.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// Asking for `url` used to be forwarded to the upstream and the provider's own
// signed link relayed back — which names the upstream and carries the
// operator's account in the path. This package hides who served a request
// everywhere else, and a link is the plainest way of saying it.
func TestImagesGenerationsNeverRelaysAProviderURL(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created":1,"data":[
		{"url":"https://provider.example/private/org-OPERATOR/img.png?sig=SECRET"}
	]}`)

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model":"painter","prompt":"x","response_format":"url"}`)

	// The caller asked for a link; it is not forwarded upstream either.
	if asked, _ := fix.upstream.received()["response_format"].(string); asked != "b64_json" {
		t.Errorf("asked the provider for %q, want b64_json", asked)
	}
	if strings.Contains(w.Body.String(), "provider.example") ||
		strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("the provider's URL reached the caller: %s", w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when nothing but links came back: %s", w.Code, w.Body.String())
	}
}

const testPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

func TestImagesGenerationsWithReferenceImagesJSON(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`)

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model":"painter","prompt":"combine","images":["`+testPNG+`","`+testPNG+`"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func TestImagesEditsMultipart(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "painter")
	_ = writer.WriteField("prompt", "edit this image")
	part, err := writer.CreateFormFile("image", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	pngBytes, _ := base64.StdEncoding.DecodeString(testPNG)
	_, _ = part.Write(pngBytes)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	req.Header.Set("Authorization", "Bearer "+fix.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	fix.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func TestImagesGenerationsTooManyReferenceImages(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)

	many := make([]string, chat.MaxReferenceImages+1)
	for i := range many {
		many[i] = `"` + testPNG + `"`
	}

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model":"painter","prompt":"too many","images":[`+strings.Join(many, ",")+`]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// zeros is an endless run of zero bytes, so a test can send a body larger
// than the cap without holding it in memory first.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// ParseMultipartForm's argument is a memory threshold, not a ceiling: past it
// the file parts go to temporary files on disk, as large as the sender likes.
// So the body itself has to be capped, or any key holder can fill the disk
// with a part this handler never even reads. The padding here is exactly
// that: a valid edit, plus one field nobody asked for.
func TestImagesEditsRefusesABodyPastTheCeiling(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`)

	pngBytes, _ := base64.StdEncoding.DecodeString(testPNG)
	reader, pipe := io.Pipe()
	writer := multipart.NewWriter(pipe)
	go func() {
		_ = writer.WriteField("model", "painter")
		_ = writer.WriteField("prompt", "edit this image")
		part, _ := writer.CreateFormFile("image", "test.png")
		_, _ = part.Write(pngBytes)
		padding, _ := writer.CreateFormFile("padding", "padding.bin")
		_, err := io.Copy(padding, io.LimitReader(zeros{}, 40<<20))
		if err == nil {
			err = writer.Close()
		}
		_ = pipe.CloseWithError(err)
	}()
	// Whatever the handler did not read is still blocked on the pipe; this
	// lets that goroutine finish once the request is over.
	defer reader.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", reader)
	req.Header.Set("Authorization", "Bearer "+fix.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	fix.mux.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", w.Code, w.Body.String())
	}
}

// The official SDKs send a list of pictures as repeated `image[]` parts, not
// `image`, so an edit with two references has to arrive upstream as two.
func TestImagesEditsTakesTheBracketedFieldName(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`)

	pngBytes, _ := base64.StdEncoding.DecodeString(testPNG)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "painter")
	_ = writer.WriteField("prompt", "combine these")
	for _, name := range []string{"a.png", "b.png"} {
		part, err := writer.CreateFormFile("image[]", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(pngBytes)
	}
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	req.Header.Set("Authorization", "Bearer "+fix.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	fix.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	if got := strings.Count(fix.upstream.receivedRaw(), `name="image"`); got != 2 {
		t.Errorf("upstream received %d reference images, want 2", got)
	}
}

// The reservation covers the n pictures that were asked for. An upstream that
// answers with more used to have every one of them billed and relayed, past
// what was reserved; the panel's endpoint already dropped the surplus.
func TestImagesGenerationsDropsWhatTheProviderSentPastN(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 2)
	fix.upstream.reply(`{"created": 1, "data": [
		{"b64_json": "aGVsbG8="}, {"b64_json": "aGVsbG8="}, {"b64_json": "aGVsbG8="}
	]}`)

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model": "painter", "prompt": "just one", "n": 1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp openAIImageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Errorf("relayed %d images, want the 1 that was asked for", len(resp.Data))
	}

	fix.mu.Lock()
	defer fix.mu.Unlock()
	if len(fix.records) != 1 {
		t.Fatalf("wrote %d ledger records, want 1", len(fix.records))
	}
	// One picture at a request weight of two, not three.
	if got := fix.records[0].Credits; got != 2 {
		t.Errorf("charged %v credits, want 2", got)
	}
}

// Clients written against OpenAI's image API send a reference picture as a
// data: URL as often as bare base64, and this endpoint reads both. The
// panel's endpoint takes bare base64 only, so this is held here rather than
// in the shared decoder.
func TestImagesGenerationsTakesADataURLReference(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`)

	w := fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model":"painter","prompt":"from this","image":"data:image/png;base64,`+testPNG+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	w = fix.do(t, http.MethodPost, "/v1/images/generations", fix.token,
		`{"model":"painter","prompt":"from this","image":"data:image/png;base64,not base64"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "could not be read") {
		t.Errorf("status = %d, want 400 saying it could not be read: %s", w.Code, w.Body.String())
	}
}

// An operator who raised the picture limit raised what an edit may send, not
// what is held in memory while its form is read: the pictures past the fixed
// threshold go to temporary files, and the edit still arrives whole.
func TestImagesEditsPastTheMemoryThresholdStillArrive(t *testing.T) {
	fix := newFixture(t)
	imageModel(t, fix, "painter", 0)
	fix.upstream.reply(`{"created":1700000000,"data":[{"b64_json":"aGVsbG8="}]}`)
	fix.handlers.MaxUploadBytes = func() int64 { return 20 << 20 }

	picture := make([]byte, 16<<20)
	copy(picture, "\x89PNG\r\n\x1a\n")
	if 2*int64(len(picture)) <= editMemoryBytes {
		t.Fatalf("the pictures fit in %d bytes and would never reach the disk", editMemoryBytes)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "painter")
	_ = writer.WriteField("prompt", "combine these")
	for _, name := range []string{"a.png", "b.png"} {
		part, err := writer.CreateFormFile("image[]", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(picture)
	}
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	req.Header.Set("Authorization", "Bearer "+fix.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	fix.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := strings.Count(fix.upstream.receivedRaw(), `name="image"`); got != 2 {
		t.Errorf("upstream received %d reference images, want 2", got)
	}
}
