package compat

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// A picture sent inline is checked the way an upload is: the ceiling, then the
// bytes themselves. The declared media type is only what the caller says, and
// it used to reach the provider unexamined.

func svgPayload() string {
	return base64.StdEncoding.EncodeToString(
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
}

func TestInlinePictureDeclaredAsSVGIsRefused(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","stream":false,"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/svg+xml;base64,` + svgPayload() + `"}}]}]}`
	if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if f.upstream.received() != nil {
		t.Error("an SVG was forwarded to the provider")
	}
}

func TestInlinePictureOverTheCeilingIsRefused(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	data := make([]byte, conversation.MaxAttachmentBytes+1)
	copy(data, "\x89PNG\r\n\x1a\n")
	body := `{"model":"` + f.model.ID + `","stream":false,"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,` +
		base64.StdEncoding.EncodeToString(data) + `"}}]}]}`
	if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if f.upstream.received() != nil {
		t.Error("an oversized picture was forwarded to the provider")
	}
}

// The provider is told the type the bytes have, not the one the caller wrote.
func TestInlinePictureIsSentAsTheTypeItsBytesHave(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","stream":false,"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,` + testPNG + `"}}]}]}`
	if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	sent, err := json.Marshal(f.upstream.received())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sent), "data:image/png;base64,") {
		t.Errorf("the provider was not sent the sniffed type: %s", sent)
	}
}

func TestAnthropicInlinePictureIsCheckedToo(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","max_tokens":16,"messages":[{"role":"user","content":[` +
		`{"type":"image","source":{"type":"base64","media_type":"image/svg+xml","data":"` + svgPayload() + `"}}]}]}`
	if w := f.do(t, http.MethodPost, "/v1/messages", f.token, body); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if f.upstream.received() != nil {
		t.Error("an SVG was forwarded to the provider")
	}
}

// A picture inside a function_call_output is refused whether or not it would
// pass the checks. An invalid one used to fail them inside the reader, and the
// reader's fallback then passed the raw JSON on as the result text, base64 and
// all.
func TestResponsesOutputPictureIsRefusedBeforeItIsChecked(t *testing.T) {
	oversized := make([]byte, conversation.MaxAttachmentBytes+1)
	copy(oversized, "\x89PNG\r\n\x1a\n")

	for name, url := range map[string]string{
		"an SVG declared as a picture": "data:image/svg+xml;base64," + svgPayload(),
		"a picture over the ceiling":   "data:image/png;base64," + base64.StdEncoding.EncodeToString(oversized),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.upstream.reply(answer)

			body := `{"model":"` + f.model.ID + `","store":false,"input":[` +
				`{"type":"message","role":"user","content":"what is on screen?"},` +
				`{"type":"function_call_output","call_id":"call_x","output":[` +
				`{"type":"input_image","image_url":"` + url + `"}]}]}`
			w := f.do(t, http.MethodPost, "/v1/responses", f.token, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "image") {
				t.Errorf("refusal does not say why: %s", w.Body.String())
			}
			if f.upstream.received() != nil {
				t.Error("a picture inside a result reached the provider")
			}
		})
	}
}
