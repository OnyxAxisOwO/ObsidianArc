package backup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// The file is named only once the body is about to begin. An error answered
// with an attachment name would be saved to disk as the export.
func TestExportEndpointNamesTheFileItStreams(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)
	f.write(t, f.account, "About lamps", "what is a lamp")

	rec := httptest.NewRecorder()
	httpx.Wrap(h.export)(rec, asUser(httptest.NewRequest(http.MethodGet, "/api/account/export", nil), f.account))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, `attachment; filename="obsidian-arc-owner-`) {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}

	var document Document
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("body is not a document: %v", err)
	}
	if len(document.Conversations) != 1 || document.Conversations[0].Title != "About lamps" {
		t.Errorf("exported %+v", document.Conversations)
	}
}

// The live chain compresses JSON, and the compressor decides on the status and
// the Content-Type at the moment the handler writes its header. The export must
// come out of it as a gzip body that decodes to the same document.
func TestExportEndpointSurvivesCompression(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)
	f.write(t, f.account, "About lamps", "what is a lamp")

	req := asUser(httptest.NewRequest(http.MethodGet, "/api/account/export", nil), f.account)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	httpx.Compress()(httpx.Wrap(h.export)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	plain, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	var document Document
	if err := json.NewDecoder(plain).Decode(&document); err != nil {
		t.Fatalf("decompressed body is not a document: %v", err)
	}
	if len(document.Conversations) != 1 || document.Conversations[0].Title != "About lamps" {
		t.Errorf("exported %+v", document.Conversations)
	}
}

// The reads fail before anything is sent, so the answer is an ordinary error
// response and carries no file name.
func TestExportEndpointAnswersAnErrorWithoutAFileName(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)
	f.write(t, f.account, "About lamps", "what is a lamp")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	httpx.Wrap(h.export)(rec, asUser(
		httptest.NewRequest(http.MethodGet, "/api/account/export", nil).WithContext(ctx), f.account))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("an error response named a file: %q", got)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %v: %s", err, rec.Body)
	}
	if body.Error.Code != "internal" {
		t.Errorf("error code %q, want internal", body.Error.Code)
	}
}

// A NUL in an uploaded file is the sender's problem, so it is a 400 with the
// reason, and nothing from the file is kept.
func TestImportEndpointRefusesNULAsBadRequest(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	h := NewHandlers(f.service)

	body := `{"obsidian_arc_export":1,"conversations":[{"title":"t","messages":[{"role":"user","content":"a\u0000b"}]}]}`
	rec := httptest.NewRecorder()
	httpx.Wrap(h.importDocument)(rec, asUser(
		httptest.NewRequest(http.MethodPost, "/api/account/import", strings.NewReader(body)), f.account))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	var reply struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("error body is not JSON: %v", err)
	}
	if reply.Error.Code != "bad_request" || !strings.Contains(reply.Error.Message, "NUL") {
		t.Errorf("error = %+v", reply.Error)
	}

	threads, err := f.conversations.List(ctx, f.account.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 0 {
		t.Errorf("a refused file left %d conversations behind", len(threads))
	}
}
