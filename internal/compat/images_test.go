package compat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImagesGenerations(t *testing.T) {
	fix := newFixture(t)
	fix.upstream.reply(`{
		"created": 1700000000,
		"data": [
			{"b64_json": "aGVsbG8=", "revised_prompt": "revised prompt"}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{
		"model": "upstream-real-name",
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
