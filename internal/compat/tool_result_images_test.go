package compat

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

func resultWithImage(modelID, data string) string {
	return fmt.Sprintf(`{"model":%q,"max_tokens":16,"messages":[
{"role":"user","content":"Check the captured screen"},
{"role":"assistant","content":[{"type":"tool_use","id":"shot_1","name":"screenshot","input":{}}]},
{"role":"user","content":[{"type":"tool_result","tool_use_id":"shot_1","content":[
{"type":"text","text":"screen captured"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}]}]}`, modelID, data)
}

func TestAnthropicToolResultImageIsForwardedWithItsResult(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	body := resultWithImage(f.model.ID, testPNG)
	w := f.do(t, http.MethodPost, "/v1/messages", f.token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	messages := f.upstream.received()["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages=%v", messages)
	}
	result := messages[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "shot_1" || result["content"] != "screen captured" {
		t.Fatalf("result=%v", result)
	}
	imageMessage := messages[3].(map[string]any)
	if imageMessage["role"] != "user" {
		t.Fatalf("image message=%v", imageMessage)
	}
	var found bool
	for _, item := range imageMessage["content"].([]any) {
		block := item.(map[string]any)
		if block["type"] == "image_url" {
			found = block["image_url"].(map[string]any)["url"] == "data:image/png;base64,"+testPNG
		}
	}
	if !found {
		t.Fatal("tool result image was lost or changed")
	}
	count := f.do(t, http.MethodPost, "/v1/messages/count_tokens", f.token, body)
	if count.Code != http.StatusOK {
		t.Fatalf("count_tokens=%d: %s", count.Code, count.Body.String())
	}
}

func TestAnthropicToolResultImageIsValidated(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		f := newFixture(t)
		w := f.do(t, http.MethodPost, path, f.token, resultWithImage(f.model.ID, "invalid base64"))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "base64") || f.upstream.received() != nil {
			t.Fatalf("%s=%d: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestTextOnlyModelDoesNotSilentlyLoseToolResultImage(t *testing.T) {
	f := newFixture(t)
	no := false
	if _, err := f.models.Update(context.Background(), f.model.ID, model.Update{SupportsImages: &no}); err != nil {
		t.Fatal(err)
	}
	w := f.do(t, http.MethodPost, "/v1/messages", f.token, resultWithImage(f.model.ID, testPNG))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "model does not accept images") || f.upstream.received() != nil {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
}
