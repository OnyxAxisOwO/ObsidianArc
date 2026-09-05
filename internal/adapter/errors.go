package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrorKind classifies an upstream failure into the handful of cases the
// layers above actually behave differently for. The gateway maps these to
// HTTP statuses and to what the user is told; nothing above this package
// parses a provider's error text.
type ErrorKind string

const (
	// The key is wrong, or is for a different service than the provider says.
	ErrorAuth ErrorKind = "auth"
	// The provider is throttling us.
	ErrorRateLimit ErrorKind = "rate_limit"
	// We sent something the provider would not accept.
	ErrorInvalidRequest ErrorKind = "invalid_request"
	// The model or endpoint would not take an attached image.
	ErrorImagesUnsupported ErrorKind = "images_unsupported"
	// The model declined to answer.
	ErrorRefusal ErrorKind = "refusal"
	// The provider failed on its own side.
	ErrorUpstream ErrorKind = "upstream"
	// We could not reach the provider at all.
	ErrorNetwork ErrorKind = "network"
	// The caller went away, or a deadline passed.
	ErrorCancelled ErrorKind = "cancelled"
)

type Error struct {
	Kind ErrorKind
	// The upstream HTTP status, when there was one.
	Status int
	// Safe to show a user: it is either our own sentence or the provider's
	// own error message, which is written for a developer rather than
	// containing our credentials.
	Message string
	// From a 429's Retry-After, when the provider sent one.
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// Retryable reports whether trying the same request again could plausibly
// work. Nothing retries automatically today — a chat turn is expensive and
// the user is watching — but the classification belongs with the error.
func (e *Error) Retryable() bool {
	return e.Kind == ErrorRateLimit || e.Kind == ErrorUpstream || e.Kind == ErrorNetwork
}

// classifyHTTP turns a response status and a decoded error body into one of
// the kinds above.
//
// The two special cases are the ones that would otherwise leave a user
// staring at a message they cannot act on:
//
//   - A 401 or 403 almost never means "the key has a typo". It means the key
//     belongs to a different service than the provider is configured as, or
//     the base URL points somewhere that wants a different credential. Saying
//     which provider it was sent as, and where, is what makes it fixable.
//   - A text-only model behind an OpenAI-compatible base URL rejects an image
//     by naming the wire format. The cause is that the model cannot see.
func classifyHTTP(p Provider, endpoint string, status int, retryAfter string, payload []byte, carriedImages bool) *Error {
	message := extractErrorMessage(payload)

	switch {
	case status == 401 || status == 403:
		if message == "" {
			message = fmt.Sprintf("The provider rejected our credentials (HTTP %d).", status)
		}
		return &Error{
			Kind:    ErrorAuth,
			Status:  status,
			Message: fmt.Sprintf("%s (sent as %s to %s)", message, p.Kind, redactURL(endpoint)),
		}

	case status == 429:
		if message == "" {
			message = "The provider is rate limiting this key."
		}
		return &Error{
			Kind:       ErrorRateLimit,
			Status:     status,
			Message:    message,
			RetryAfter: parseRetryAfter(retryAfter),
		}

	case carriedImages && (status == 400 || status == 415 || status == 422):
		return &Error{
			Kind:    ErrorImagesUnsupported,
			Status:  status,
			Message: strings.TrimSpace("This model or endpoint did not accept an attached image. " + message),
		}

	case status >= 500:
		if message == "" {
			message = fmt.Sprintf("The provider returned HTTP %d.", status)
		}
		return &Error{Kind: ErrorUpstream, Status: status, Message: message}

	default:
		if message == "" {
			message = fmt.Sprintf("The provider rejected the request (HTTP %d).", status)
		}
		return &Error{Kind: ErrorInvalidRequest, Status: status, Message: message}
	}
}

// networkError wraps a transport failure, keeping cancellation distinct: a
// user pressing Stop is not an incident, and must not be logged as one.
func networkError(ctx context.Context, err error) *Error {
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return &Error{Kind: ErrorCancelled, Message: "The request was cancelled.", cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: ErrorNetwork, Message: "The provider did not respond in time.", cause: err}
	}
	return &Error{
		Kind:    ErrorNetwork,
		Message: "Could not reach the provider.",
		cause:   err,
	}
}

// A URL is shown to an administrator to explain where a key was sent, so any
// credential carried in it has to go. Providers do not normally use query
// credentials, but a hand-written base URL might.
func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	if parsed.RawQuery != "" {
		parsed.RawQuery = "…"
	}
	return parsed.String()
}

func parseRetryAfter(value string) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(trimmed); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := time.Parse(time.RFC1123, trimmed); err == nil {
		if wait := time.Until(when); wait > 0 {
			return wait
		}
	}
	return 0
}
