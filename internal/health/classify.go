package health

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// Code turns an upstream failure into the short token the page groups by.
//
// Deliberately coarse. An operator scanning a list wants "the key is wrong"
// and "it is rate limiting us" to each be one line, not one line per minute
// with a different request id in it.
func Code(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	// A transport timeout is recognised by its type. The text of a net error
	// is the operating system's wording, which is not stable across platforms.
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}

	// The typed kind and status are what the provider said, so they decide.
	// Matching the rendered text got this wrong both ways: an adapter error
	// renders as "kind: message" with no status, so a 401 whose body said
	// "Incorrect API key" matched nothing, and a 400 quoting "5000 tokens"
	// matched "500".
	var upstream *adapter.Error
	if errors.As(err, &upstream) {
		return codeOfAdapterError(upstream)
	}

	message := strings.ToLower(err.Error())
	for _, rule := range codeRules {
		if rule.matches(message) {
			return rule.code
		}
	}
	return "error"
}

// codeRule is one row of the text classification: the status numbers and the
// words that name the same failure. Rules are tried in order and the first one
// to match wins, so a message naming both a 401 and a rate limit stays an
// authorisation failure, as it has always been.
type codeRule struct {
	code    string
	numbers []string
	words   []string
}

// codeRules is the only list of status numbers Code recognises. The patterns
// below are built from it, so a number cannot be classified here and be
// missing from the compiled set, which used to be a nil pattern and a panic.
var codeRules = []codeRule{
	{code: "unauthorized", numbers: []string{"401"}, words: []string{"unauthorized", "invalid api key"}},
	{code: "forbidden", numbers: []string{"403"}, words: []string{"forbidden"}},
	{code: "model_not_found", numbers: []string{"404"}, words: []string{"not found"}},
	{code: "rate_limited", numbers: []string{"429"}, words: []string{"rate limit"}},
	{code: "timeout", words: []string{"timeout", "deadline"}},
	{code: "unreachable", words: []string{"no such host", "connection refused", "dial "}},
	{code: "upstream_error", numbers: []string{"500", "502", "503", "504"}},
}

// statusPatterns holds one pattern per status number in codeRules, compiled
// once: Code runs on every failed probe, and compiling a pattern per call cost
// more than the match.
var statusPatterns = func() map[string]*regexp.Regexp {
	patterns := map[string]*regexp.Regexp{}
	for _, rule := range codeRules {
		for _, number := range rule.numbers {
			patterns[number] = regexp.MustCompile(`(?:^|\D)` + number + `(?:\D|$)`)
		}
	}
	return patterns
}()

// matches reports whether a lower-cased message names this rule's failure.
func (r codeRule) matches(message string) bool {
	for _, number := range r.numbers {
		if hasNumber(message, number) {
			return true
		}
	}
	for _, word := range r.words {
		if strings.Contains(message, word) {
			return true
		}
	}
	return false
}

// hasNumber reports whether a status code appears as a number of its own. A
// plain substring match read "4290" as a 429 and "5000" as a 500, which is how
// a message quoting a token count was filed as a rate limit.
func hasNumber(message, number string) bool {
	return statusPatterns[number].MatchString(message)
}

// codeOfAdapterError maps the provider failure the adapter already classified.
// An auth error with a 403 is a key for another service, which the operator
// reads differently from a wrong key, so the status is consulted there.
func codeOfAdapterError(e *adapter.Error) string {
	switch e.Kind {
	case adapter.ErrorAuth:
		if e.Status == http.StatusForbidden {
			return "forbidden"
		}
		return "unauthorized"
	case adapter.ErrorRateLimit:
		return "rate_limited"
	case adapter.ErrorNetwork:
		return "unreachable"
	case adapter.ErrorUpstream:
		return "upstream_error"
	case adapter.ErrorCancelled:
		return "cancelled"
	}
	if e.Status == http.StatusNotFound {
		return "model_not_found"
	}
	return "error"
}
