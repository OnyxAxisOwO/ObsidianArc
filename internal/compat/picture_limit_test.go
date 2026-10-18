package compat

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// inlinePNG is a data URL of the given size whose first bytes are a PNG
// signature. The size check and the media check look at nothing else, so the
// picture reaches them without a real image being encoded.
func inlinePNG(size int) string {
	data := make([]byte, size)
	copy(data, "\x89PNG\r\n\x1a\n")
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

func chatWithPicture(modelID, picture string) string {
	return `{"model":"` + modelID + `","stream":false,"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is this"},` +
		`{"type":"image_url","image_url":{"url":"` + picture + `"}}]}]}`
}

// The operator's picture limit governs /v1 the way it governs the browser. A
// picture over the limit is refused, and the refusal names the limit that was
// actually hit rather than the built-in six megabytes.
func TestOperatorPictureLimitGovernsV1(t *testing.T) {
	f := newFixture(t)
	f.handlers.MaxUploadBytes = func() int64 { return 1 << 20 }

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		chatWithPicture(f.model.ID, inlinePNG(2<<20)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "at most 1 MB") {
		t.Errorf("refusal does not name the operator's limit: %s", w.Body.String())
	}
}

// Raising the limit lets /v1 take a picture the built-in six megabytes refuse.
// The JSON body cap grows with it, or the picture could never arrive: ten
// megabytes of picture is about fourteen megabytes of base64.
func TestOperatorPictureLimitCanBeRaised(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)
	f.handlers.MaxUploadBytes = func() int64 { return 10 << 20 }

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		chatWithPicture(f.model.ID, inlinePNG(10<<20)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// Without an operator setting the limits are the built-in ones, so an install
// that never touched the setting behaves as it did before the setting was read.
func TestPictureLimitDefaultsAreUnchanged(t *testing.T) {
	h := &Handlers{}
	if got := h.pictureCeiling(); got != 6<<20 {
		t.Errorf("default picture ceiling = %d, want %d", got, 6<<20)
	}
	if got := h.bodyLimit(); got != 12<<20 {
		t.Errorf("default body limit = %d, want %d", got, 12<<20)
	}

	h.MaxUploadBytes = func() int64 { return 0 }
	if got := h.pictureCeiling(); got != 6<<20 {
		t.Errorf("a zero setting ceiling = %d, want the default %d", got, 6<<20)
	}
}
