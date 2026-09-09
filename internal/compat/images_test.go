package compat

import (
	"context"
	"encoding/json"
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
