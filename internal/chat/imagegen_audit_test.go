package chat

import (
	"net/http"
	"strings"
	"testing"
)

// The panel stores each prompt, size and style with its picture and lists them
// back a hundred at a time. A bound on what a chat message may hold is the
// same bound the library needs, and a value past it is refused before the
// provider is asked for anything.
func TestAnImageRequestWithAnOversizedFieldIsRefusedBeforeTheProviderIsCalled(t *testing.T) {
	cases := map[string]string{
		"prompt": `{"prompt":"` + strings.Repeat("x", 32001) + `"}`,
		"size":   `{"prompt":"a sunset","size":"` + strings.Repeat("9", 65) + `"}`,
		"style":  `{"prompt":"a sunset","style":"` + strings.Repeat("s", 65) + `"}`,
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			lab := newImageLab(t, 0)
			lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

			body := strings.Replace(fields, "{", `{"model_id":"`+lab.painter.ID+`",`, 1)
			recorder := lab.generate(t, body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if lab.fixture.upstream.lastRequest() != nil {
				t.Fatal("the provider was called for a refused request")
			}
			if records := lab.ledger(); len(records) != 0 {
				t.Fatalf("wrote %d ledger records for a refused request", len(records))
			}
		})
	}
}

// A prompt at the limit is still a prompt: the bound is on the size of a
// message, not a new rule about what a picture may be asked for.
func TestAnImageRequestAtTheLimitIsStillGenerated(t *testing.T) {
	lab := newImageLab(t, 0)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	body := `{"model_id":"` + lab.painter.ID + `","prompt":"` + strings.Repeat("x", 32000) + `","size":"1024x1024"}`
	if recorder := lab.generate(t, body); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}
