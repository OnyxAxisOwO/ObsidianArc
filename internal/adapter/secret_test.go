package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A key shaped like the real thing, so the redaction runs on what an operator
// would actually have pasted.
const secretKey = "sk-test-0123456789abcdef"

// A provider that answers a bad request by quoting the credential back is the
// case both the administrator and the chat user read. The message keeps its
// wording and loses only the key.
func TestUpstreamErrorDoesNotEchoTheKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, secretKey)
	}))
	defer server.Close()

	var out collected
	_, err := testRegistry().Chat(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: secretKey},
		ChatRequest{Model: testModel(), Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
		out.sink)

	var upstream *Error
	if !errors.As(err, &upstream) {
		t.Fatalf("want an adapter error, got %v", err)
	}
	if want := "Incorrect API key provided: ***"; upstream.Message != want {
		t.Errorf("message = %q, want %q", upstream.Message, want)
	}
	if strings.Contains(err.Error(), secretKey) {
		t.Errorf("the key reached the error text: %v", err)
	}
}

func TestErrorMessageRemovesTheKey(t *testing.T) {
	// The key starts 390 characters into the body and runs past the 400
	// character cut. Removed before the cut, none of it survives as a prefix.
	straddling := strings.Repeat("x", 390) + secretKey + "tail"

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "json message",
			body: `{"error":{"message":"bad key ` + secretKey + `"}}`,
			want: "bad key ***",
		},
		{
			// Decoded before the key is looked for, so a body that spells the
			// same characters as JSON escapes is caught too.
			name: "json escapes",
			body: `{"error":{"message":"bad key sk-test-0123456789abcdef"}}`,
			want: "bad key ***",
		},
		{
			name: "plain body",
			body: "upstream echoed " + secretKey,
			want: "upstream echoed ***",
		},
		{
			name: "key straddles the cut",
			body: straddling,
			want: strings.Repeat("x", 390) + "***tail",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractErrorMessage([]byte(tc.body), secretKey); got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
		})
	}
}

// An empty key must change nothing. Replacing "" would put *** between every
// character, and a provider with no key stored would then garble every error.
func TestEmptyKeyChangesNothing(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"plain boom"}}`,
		"plain boom",
	} {
		if got := extractErrorMessage([]byte(body), ""); got != "plain boom" {
			t.Errorf("extractErrorMessage(%q, \"\") = %q, want %q", body, got, "plain boom")
		}
	}
}

// The endpoint is part of an auth message. A base URL that carries the key in
// its path would otherwise print it there.
func TestAuthMessageDoesNotPrintTheKeyFromTheEndpoint(t *testing.T) {
	err := classifyHTTP(
		Provider{Kind: KindOpenAI, APIKey: secretKey},
		"https://gateway.example.com/"+secretKey+"/v1/chat/completions",
		401, "", nil, false)

	if strings.Contains(err.Message, secretKey) {
		t.Errorf("auth message prints the key: %q", err.Message)
	}
	if !strings.Contains(err.Message, "gateway.example.com") {
		t.Errorf("auth message no longer names the endpoint: %q", err.Message)
	}
}

// A streamed answer can end with an error event that quotes the credential.
// That message leaves the adapter like any other, so it is redacted too.
func TestStreamedErrorDoesNotEchoTheKey(t *testing.T) {
	cases := []struct {
		name  string
		kind  Kind
		frame string
	}{
		{
			name:  "openai",
			kind:  KindOpenAI,
			frame: `{"error":{"message":"rejected key ` + secretKey + `"}}`,
		},
		{
			name:  "anthropic",
			kind:  KindAnthropic,
			frame: `{"type":"error","error":{"type":"overloaded_error","message":"rejected key ` + secretKey + `"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := sseServer(t, []string{tc.frame}, nil)

			var out collected
			_, err := testRegistry().Chat(context.Background(),
				Provider{Kind: tc.kind, BaseURL: server.URL, APIKey: secretKey},
				ChatRequest{Model: testModel(), Stream: true, Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "hi"}}}}},
				out.sink)

			var upstream *Error
			if !errors.As(err, &upstream) {
				t.Fatalf("want an adapter error, got %v", err)
			}
			if want := "rejected key ***"; upstream.Message != want {
				t.Errorf("message = %q, want %q", upstream.Message, want)
			}
		})
	}
}
