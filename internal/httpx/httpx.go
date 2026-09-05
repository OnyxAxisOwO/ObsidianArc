// Package httpx is the transport layer every module's handlers share: one
// error shape, one JSON encoder, one request decoder, one SSE writer.
//
// The rule it exists to enforce is that a handler returns an error and
// something else decides what the client sees. A handler that writes its own
// status codes and its own error bodies is how a project ends up leaking a
// database message to a browser in one place and swallowing a real failure in
// another.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// Error is the only failure a handler should return when it wants to control
// what the client sees. Anything else becomes a 500 with a generic body and a
// logged cause.
type Error struct {
	Status int
	// A stable machine-readable token the frontend can branch on
	// ("quota_exceeded", "invalid_credentials"). Never localise this; the
	// message is what gets translated.
	Code    string
	Message string
	// Extra fields merged into the error object — the reset time on a quota
	// rejection, the field name on a validation failure.
	Details map[string]any
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// WithDetails returns a copy carrying extra fields. Copying rather than
// mutating keeps the package-level sentinels below safe to share.
func (e *Error) WithDetails(details map[string]any) *Error {
	clone := *e
	clone.Details = details
	return &clone
}

// WithCause attaches the underlying failure for the log without changing what
// the client is told.
func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.cause = err
	return &clone
}

func newError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(format string, args ...any) *Error {
	return newError(http.StatusBadRequest, "bad_request", fmt.Sprintf(format, args...))
}

func Unauthorized(message string) *Error {
	return newError(http.StatusUnauthorized, "unauthorized", message)
}

func Forbidden(message string) *Error {
	return newError(http.StatusForbidden, "forbidden", message)
}

func NotFound(message string) *Error {
	return newError(http.StatusNotFound, "not_found", message)
}

func Conflict(code, message string) *Error {
	return newError(http.StatusConflict, code, message)
}

func TooManyRequests(code, message string) *Error {
	return newError(http.StatusTooManyRequests, code, message)
}

func Unavailable(message string) *Error {
	return newError(http.StatusServiceUnavailable, "unavailable", message)
}

// Internal wraps a failure the client should learn nothing about.
func Internal(err error) *Error {
	return (&Error{
		Status:  http.StatusInternalServerError,
		Code:    "internal",
		Message: "Something went wrong on our side.",
	}).WithCause(err)
}

// Handler is a http.HandlerFunc that may fail. Wrap turns one into the
// standard library's shape.
type Handler func(http.ResponseWriter, *http.Request) error

func Wrap(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}

func WriteJSON(w http.ResponseWriter, status int, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}

func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// WriteError renders err as the standard error body. A 5xx is logged with its
// real cause; a 4xx is not, because a client sending bad input is not an
// incident.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		apiErr = Internal(err)
	}

	if apiErr.Status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed",
			"error", err,
			"method", r.Method,
			"path", r.URL.Path,
			"request_id", RequestIDFrom(r.Context()),
		)
	}

	body := map[string]any{"code": apiErr.Code, "message": apiErr.Message}
	for key, value := range apiErr.Details {
		body[key] = value
	}

	// A response already committed cannot carry an error body — most often a
	// stream that failed halfway. The log above is the whole record.
	if Committed(w) {
		return
	}
	_ = WriteJSON(w, apiErr.Status, map[string]any{"error": body})
}

// DecodeJSON reads a request body into dst with a hard size cap and strict
// field checking, so a typo in a client payload is an error rather than a
// silently ignored field.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &Error{
				Status:  http.StatusRequestEntityTooLarge,
				Code:    "payload_too_large",
				Message: fmt.Sprintf("Request body must be under %d bytes.", maxBytes),
			}
		}
		if errors.Is(err, io.EOF) {
			return BadRequest("Request body is empty.")
		}
		return BadRequest("Request body is not valid JSON: %s", err.Error())
	}
	// A second JSON value in the same body is a sign of a confused client, and
	// accepting it silently hides that.
	if decoder.More() {
		return BadRequest("Request body must contain a single JSON object.")
	}
	return nil
}

// ClientIP resolves the caller's address. Forwarded headers are honoured only
// when the operator has said a proxy is in front, because anyone can send
// them otherwise and rate limiting keyed on a spoofable value is worse than
// none.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			if first, _, ok := strings.Cut(forwarded, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(forwarded)
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-Ip")); real != "" {
			return real
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestIDFrom(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey).(string); ok {
		return value
	}
	return ""
}

func withRequestID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, requestIDKey, value)
}
