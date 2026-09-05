package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Shared plumbing: base-URL handling, the Server-Sent Events reader, inline
// reasoning extraction, and the error-body parser. Both adapters use all of
// it; none of it is specific to either protocol.

// MaxErrorBodyBytes bounds how much of a failed response is read. A provider
// returning a megabyte of HTML on an error should not cost us a megabyte of
// memory per failed request.
const MaxErrorBodyBytes = 32 * 1024

// NormalizeBaseURL validates the address a provider's key will be sent to.
//
// It is validated rather than interpolated as typed because it is the
// destination of a credential: a mistyped scheme downgrades the key to
// plaintext on the wire, and embedded credentials in the URL would end up in
// logs. Plain http is allowed only for loopback, which is how a local Ollama
// or vLLM is reached.
func NormalizeBaseURL(raw string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return "", fmt.Errorf("base URL is required")
	}
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %s", raw)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid base URL: %s", raw)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("base URL must not embed credentials")
	}

	host := parsed.Hostname()
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return "", fmt.Errorf("base URL must use https (http is allowed only for localhost)")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// Providers document the full endpoint rather than a base, so that is what
// people paste. Every partial form is accepted, because there is no way to
// tell a user which of the three they should have entered.
func chatEndpoint(kind Kind, base string) string {
	trimmed := strings.TrimRight(base, "/")
	if kind == KindAnthropic {
		switch {
		case strings.HasSuffix(trimmed, "/messages"):
			return trimmed
		case strings.HasSuffix(trimmed, "/v1"):
			return trimmed + "/messages"
		default:
			return trimmed + "/v1/messages"
		}
	}
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	return trimmed + "/chat/completions"
}

func modelsEndpoint(kind Kind, base string) string {
	trimmed := strings.TrimRight(base, "/")
	if kind == KindAnthropic {
		root := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(trimmed, "/v1/messages"), "/messages"), "/v1")
		return root + "/v1/models?limit=200"
	}
	return strings.TrimSuffix(trimmed, "/chat/completions") + "/models"
}

func applyHeaders(req *http.Request, p Provider) {
	// The provider's own extras go on first, so the protocol headers below
	// cannot be overridden into something that breaks the request.
	for name, value := range p.Headers {
		if name == "" {
			continue
		}
		req.Header.Set(name, value)
	}

	if p.Kind == KindAnthropic {
		req.Header.Set("X-Api-Key", p.APIKey)
		version := p.AnthropicVersion
		if version == "" {
			version = defaultAnthropicVersion
		}
		req.Header.Set("Anthropic-Version", version)
	} else {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	req.Header.Set("Accept-Encoding", "identity")
}

func postJSON(ctx context.Context, client *http.Client, p Provider, endpoint string, body any) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidRequest, Message: "Could not encode the request.", cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidRequest, Message: "Invalid provider endpoint.", cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	applyHeaders(req, p)

	response, err := client.Do(req)
	if err != nil {
		return nil, networkError(ctx, err)
	}
	return response, nil
}

// extractErrorMessage digs the human-readable part out of an error body.
// Both protocols nest it, and several compatible servers invent their own
// shape, so a few likely shapes are tried before falling back to the raw
// text.
func extractErrorMessage(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}

	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil {
		for _, candidate := range []string{envelope.Error.Message, envelope.Message, envelope.Detail, envelope.Error.Type} {
			if trimmed := strings.TrimSpace(candidate); trimmed != "" {
				return trimmed
			}
		}
	}

	// Some gateways answer with `{"error": "a string"}`.
	var stringly struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &stringly); err == nil && strings.TrimSpace(stringly.Error) != "" {
		return strings.TrimSpace(stringly.Error)
	}

	text := strings.TrimSpace(string(payload))
	// An HTML error page (a proxy, a login wall) is noise in a chat bubble.
	if strings.HasPrefix(text, "<") {
		return ""
	}
	if len(text) > 400 {
		return text[:400] + "…"
	}
	return text
}

func readErrorBody(response *http.Response) []byte {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, MaxErrorBodyBytes))
	return payload
}

// --- server-sent events -----------------------------------------------------

// readEventStream walks an SSE response, handing each `data:` payload to fn.
//
// Hand-rolled because both protocols use only the `data` field of the format,
// and because the framing needs to tolerate `\r\n` as well as `\n`: some
// proxies rewrite line endings, and a parser that only knows about `\n` sees
// one enormous never-ending event.
//
// Returning an error from fn stops the read, which is how a disconnected
// client ends an upstream generation.
func readEventStream(body io.Reader, fn func(data []byte) error) error {
	scanner := bufio.NewScanner(body)
	// A single event can carry a large reasoning delta; the default 64 KiB
	// ceiling is not enough for every provider.
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		data, found := strings.CutPrefix(line, "data:")
		if !found {
			// `event:` and `id:` lines carry nothing either protocol needs;
			// the payload itself names its own type.
			continue
		}
		trimmed := strings.TrimSpace(data)
		if trimmed == "" || trimmed == "[DONE]" {
			continue
		}
		if err := fn([]byte(trimmed)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// --- inline reasoning --------------------------------------------------------

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// splitThinking separates reasoning that a model emitted inline at the head
// of its answer, which is what several reasoning models do on an
// OpenAI-compatible endpoint instead of using a dedicated field.
//
// It has to work on a partial buffer, because it runs on every delta: a
// stream can stop anywhere, including inside the tag. Everything after an
// unclosed <think> is reasoning — the answer has not started yet.
func splitThinking(buffer string) (reasoning, answer string) {
	open := strings.Index(buffer, thinkOpen)
	if open < 0 {
		return "", buffer
	}
	rest := buffer[open+len(thinkOpen):]
	close := strings.Index(rest, thinkClose)
	if close < 0 {
		return rest, buffer[:open]
	}
	return rest[:close], buffer[:open] + rest[close+len(thinkClose):]
}
