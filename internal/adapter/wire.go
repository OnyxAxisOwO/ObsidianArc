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
	"sync/atomic"
	"time"
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
// logs. Plain http is allowed for loopback, which is how a local Ollama or
// vLLM is reached, and otherwise only when the administrator has opted that
// one provider into it: a self-hosted endpoint on a public address with no
// certificate is a real deployment, but not one to arrive at by typo.
func NormalizeBaseURL(raw string, allowInsecure bool) (string, error) {
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
	// The opt-in widens http and nothing else: any other scheme is still a
	// mistake, and an address typed without one still defaults to https.
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (loopback || allowInsecure)) {
		return "", fmt.Errorf("base URL must use https (plain http needs the provider's own opt-in, and is always allowed for localhost)")
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

func imagesEndpoint(base string) string {
	return imagesAction(base, "generations")
}

// imageEditsEndpoint is where a picture is sent to be worked from, which is a
// sibling of the generations endpoint rather than a parameter on it.
func imageEditsEndpoint(base string) string {
	return imagesAction(base, "edits")
}

func imagesAction(base, action string) string {
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		trimmed = strings.TrimSuffix(trimmed, "/chat/completions")
	}
	// A base URL that already names one action is pointed at the sibling
	// rather than having a second path appended to it.
	if strings.HasSuffix(trimmed, "/images/generations") {
		return strings.TrimSuffix(trimmed, "generations") + action
	}
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/images/" + action
	}
	if !strings.Contains(trimmed, "/v1") {
		return trimmed + "/v1/images/" + action
	}
	return trimmed + "/images/" + action
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
	return post(ctx, client, p, endpoint, "application/json", encoded)
}

// post sends an already-encoded body. postJSON is this with the encoding in
// front of it; the image edits endpoint takes multipart, which is the only
// reason the two are separate.
func post(
	ctx context.Context, client *http.Client, p Provider, endpoint, contentType string, encoded []byte,
) (*http.Response, error) {
	send, timedOut, done := providerDeadline(ctx, p)

	req, err := http.NewRequestWithContext(send, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		done(nil, err)
		return nil, &Error{Kind: ErrorInvalidRequest, Message: "Invalid provider endpoint.", cause: err}
	}
	req.Header.Set("Content-Type", contentType)
	applyHeaders(req, p)

	response, err := client.Do(req)
	if err != nil {
		done(nil, err)
		if timedOut() {
			return nil, &Error{
				Kind:    ErrorNetwork,
				Message: "The provider did not start responding within its configured timeout.",
				cause:   err,
			}
		}
		return nil, networkError(ctx, err)
	}
	// The headers are here, so the provider is answering. Everything from now
	// on is the caller's to bound, and the deadline is handed to the body so
	// closing the response releases it.
	return done(response, nil), nil
}

// providerDeadline bounds how long a provider may take to *start* answering.
//
// The per-provider timeout an operator sets in the backoffice used to be read
// from the database, carried on Provider, and then used by nothing at all —
// the control saved, and the request still hung for as long as the browser
// held it. It is applied here rather than as a client-level Timeout because a
// deadline over the whole call would cut a long streamed answer off
// mid-sentence, which is exactly why this package sets no such Timeout.
//
// So the timer is stopped once the headers land, and its cancel is handed to
// the response body: reading the stream keeps the context alive for as long
// as the caller wants, and closing the body releases it.
//
// It narrows, it does not widen. The transport's own ResponseHeaderTimeout
// (UPSTREAM_HEADER_TIMEOUT, 90s by default) bounds the same event, so the
// smaller of the two always wins and a per-provider value above it has no
// effect — including the 120s this field defaults to. Only a provider timeout
// set below the global one changes anything.
func providerDeadline(ctx context.Context, p Provider) (
	send context.Context, timedOut func() bool, done func(*http.Response, error) *http.Response,
) {
	if p.Timeout <= 0 {
		return ctx, func() bool { return false },
			func(r *http.Response, _ error) *http.Response { return r }
	}

	timed, cancel := context.WithCancel(ctx)
	var fired atomic.Bool
	timer := time.AfterFunc(p.Timeout, func() {
		fired.Store(true)
		cancel()
	})

	// done runs on every way out, not only the successful one: a request that
	// fails in a millisecond against a refused port used to leave the timer
	// armed for the whole configured timeout, holding a context alive with it.
	return timed, fired.Load, func(response *http.Response, err error) *http.Response {
		stopped := timer.Stop()
		if err != nil || response == nil {
			cancel()
			return response
		}
		if !stopped {
			// The timer has already fired, or is about to: the context is
			// cancelled either way, so the body is dead and the caller will
			// find out on its first read.
			cancel()
			return response
		}
		response.Body = &cancelOnClose{ReadCloser: response.Body, cancel: cancel}
		return response
	}
}

// cancelOnClose releases a request context when the caller is finished with
// the stream it belongs to.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
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
// One shot, for a response that arrived whole. A stream uses inlineThinking
// below, which is the same rule with a memory of where it has already looked.
func splitThinking(buffer string) (reasoning, answer string) {
	var once inlineThinking
	return once.split(buffer)
}

// inlineThinking is that split, kept across the deltas of one answer.
//
// It has to be re-derived on every delta because a stream can stop anywhere,
// including inside the tag — but re-deriving it from the top each time makes
// the *ordinary* answer the most expensive case there is. A buffer with no
// tag in it cannot be ruled out until it has been read to the end, so every
// delta rescans everything received so far, and the cost is quadratic in the
// length of the answer. Measured on a 256 kB answer arriving four bytes at a
// time: 159 ms of scanning with no tag present, against 3 ms with one, where
// the search stops at the first byte.
//
// So each scan resumes where the last one stopped, backing off by one tag's
// width because a tag can straddle two deltas, and each offset is kept once
// it is known.
type inlineThinking struct {
	openAt      int
	openFound   bool
	openScanned int

	// Measured from the end of the opening tag rather than from the buffer,
	// which is where the closing one is looked for.
	closeAt      int
	closeFound   bool
	closeScanned int
}

// Enough to cover a tag split across two deltas: the longer of the two is
// eight bytes, so resuming that far back cannot step over one.
const tagStraddle = len(thinkClose)

func (s *inlineThinking) split(buffer string) (reasoning, answer string) {
	if !s.openFound {
		from := min(max(0, s.openScanned-tagStraddle), len(buffer))
		index := strings.Index(buffer[from:], thinkOpen)
		if index < 0 {
			s.openScanned = len(buffer)
			return "", buffer
		}
		s.openAt, s.openFound = from+index, true
	}

	rest := buffer[s.openAt+len(thinkOpen):]
	if !s.closeFound {
		from := min(max(0, s.closeScanned-tagStraddle), len(rest))
		index := strings.Index(rest[from:], thinkClose)
		if index < 0 {
			s.closeScanned = len(rest)
			return rest, buffer[:s.openAt]
		}
		s.closeAt, s.closeFound = from+index, true
	}
	return rest[:s.closeAt], buffer[:s.openAt] + rest[s.closeAt+len(thinkClose):]
}
