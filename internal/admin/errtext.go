package admin

import (
	"errors"
	"strconv"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Store and service errors are namespaced for the log ("user: …"), which is
// right there and noise to a person reading the answer. The two helpers below
// are the two ways this package takes the namespace off, kept apart because
// they answer different questions: whether a message carries one of a known
// set of packages, or whatever package it carries.

// trimPackagePrefix strips the first of prefixes that message begins with. A
// message that is nothing but a prefix comes back empty; trimSentence guards
// that case itself, because it would rather show the bare prefix than nothing.
func trimPackagePrefix(message string, prefixes ...string) string {
	for _, prefix := range prefixes {
		if rest, found := strings.CutPrefix(message, prefix); found {
			return rest
		}
	}
	return message
}

// trimAnyPackagePrefix strips everything up to the first ": ", whichever
// package wrote it. The import report reads errors from several stores, and
// a list an operator is reading does not need to know which one refused.
func trimAnyPackagePrefix(message string) string {
	if _, rest, found := strings.Cut(message, ": "); found {
		return rest
	}
	return message
}

// responseOr answers with the *httpx.Error err carries when it carries one —
// a refusal worded inside a transaction or a check is already the answer —
// and with otherwise(err) when it does not.
func responseOr(err error, otherwise func(error) error) error {
	var response *httpx.Error
	if errors.As(err, &response) {
		return response
	}
	return otherwise(err)
}

// internalError is httpx.Internal as the func(error) error responseOr takes.
func internalError(err error) error { return httpx.Internal(err) }

// queryNumber reads a non-negative integer query parameter, falling back to
// the default when it is absent, malformed, negative or out of range for T.
//
// A fallback rather than a 400 because these are paging and window bounds a
// link or an old client may carry stale: the page that answers with the
// default is more use than a refusal, and every listing has always answered
// that way.
func queryNumber[T int | int64](raw string, fallback T) T {
	if raw == "" {
		return fallback
	}
	var zero T
	bits := strconv.IntSize
	if _, wide := any(zero).(int64); wide {
		bits = 64
	}
	value, err := strconv.ParseInt(raw, 10, bits)
	if err != nil || value < 0 {
		return fallback
	}
	return T(value)
}

func intParam(raw string, fallback int) int { return queryNumber(raw, fallback) }

func int64Param(raw string, fallback int64) int64 { return queryNumber(raw, fallback) }
