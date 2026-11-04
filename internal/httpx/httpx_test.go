package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A body is one JSON object and nothing after it but whitespace. The check
// after the value used to be Decoder.More, which says nothing about a stray
// closing bracket, so a body with a trailing } or ] was accepted.
func TestDecodeJSONRejectsAnythingAfterTheObject(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"plain object", `{"a":1}`, true},
		{"trailing whitespace", "{\"a\":1}\n  \t", true},
		{"trailing brace", `{"a":1}}`, false},
		{"trailing bracket", `{"a":1}]`, false},
		{"second object", `{"a":1}{"a":2}`, false},
		{"trailing number", `{"a":1} 2`, false},
		{"trailing garbage", `{"a":1} x`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			var dst map[string]any
			err := DecodeJSON(httptest.NewRecorder(), request, &dst, 0)
			if tc.ok && err != nil {
				t.Fatalf("DecodeJSON(%q) = %v, want accepted", tc.body, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("DecodeJSON(%q) accepted a body with more than one value", tc.body)
			}
		})
	}
}

// The value fits under the cap and the padding after it runs past the limit,
// so the cap is hit by the trailing check, not by Decode. That is a body that is
// too large; answering it as malformed sent clients to fix the wrong thing.
func TestDecodeJSONPaddingPastTheCapIsTooLarge(t *testing.T) {
	body := `{"a":1}` + strings.Repeat(" ", 200)
	request := httptest.NewRequest("POST", "/", strings.NewReader(body))
	var dst map[string]any
	err := DecodeJSON(httptest.NewRecorder(), request, &dst, 64)

	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("DecodeJSON = %v, want an *Error", err)
	}
	if apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d for a body over its cap", apiErr.Status, http.StatusRequestEntityTooLarge)
	}
}
