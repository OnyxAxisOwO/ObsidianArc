package admin

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Messages each call site has to answer the same way it did before the
// prefix stripping moved into one place: bare prefixes, a prefix with nothing
// but a colon after it, two prefixes in a row, unknown packages, and text
// that only looks like a prefix further along.
var prefixSamples = []string{
	"", "user: ", "user:", "user: x", "user: invalid username", "group: name taken",
	"auth: Already set", "conversation: gone", "provider: ", "provider: invalid base URL",
	"provider: base URL: must be https", "user: group: nested", "other: thing",
	"no prefix here", "a: b: c", ": leading", "User: capitalised", "über: unicode",
	"zebra", "user:  two spaces",
}

// The rules as they were written at each site before the helpers existed.
func trimSentenceBefore(message string) string {
	for _, prefix := range []string{"user: ", "group: ", "auth: ", "conversation: "} {
		if len(message) > len(prefix) && message[:len(prefix)] == prefix {
			message = message[len(prefix):]
			break
		}
	}
	if message == "" {
		return message
	}
	if message[0] >= 'a' && message[0] <= 'z' {
		return string(message[0]-32) + message[1:]
	}
	return message
}

func plainErrorBefore(err error) string {
	message := err.Error()
	if _, rest, found := strings.Cut(message, ": "); found {
		return rest
	}
	return message
}

func TestTrimSentenceAnswersAsItDidBefore(t *testing.T) {
	for _, sample := range prefixSamples {
		if got, want := trimSentence(sample), trimSentenceBefore(sample); got != want {
			t.Errorf("trimSentence(%q) = %q, want %q", sample, got, want)
		}
	}
	// And through the call site that uses it.
	answer := translateUserError(user.ErrInvalidUsername)
	var response *httpx.Error
	if !errors.As(answer, &response) || response.Message != trimSentenceBefore(user.ErrInvalidUsername.Error()) {
		t.Errorf("translateUserError(ErrInvalidUsername) = %v", answer)
	}
}

func TestPlainErrorAnswersAsItDidBefore(t *testing.T) {
	for _, sample := range prefixSamples {
		err := errors.New(sample)
		if got, want := plainError(err), plainErrorBefore(err); got != want {
			t.Errorf("plainError(%q) = %q, want %q", sample, got, want)
		}
	}
}

func TestProviderBaseURLRefusalAnswersAsItDidBefore(t *testing.T) {
	for _, sample := range prefixSamples {
		if got, want := trimPackagePrefix(sample, "provider: "), strings.TrimPrefix(sample, "provider: "); got != want {
			t.Errorf("trimPackagePrefix(%q, provider) = %q, want %q", sample, got, want)
		}
	}
	for _, err := range []error{
		provider.ErrInvalidBaseURL,
		fmt.Errorf("%w: must be https", provider.ErrInvalidBaseURL),
	} {
		var response *httpx.Error
		if !errors.As(translateProviderError(err), &response) {
			t.Fatalf("translateProviderError(%v) is not an answer", err)
		}
		if want := strings.TrimPrefix(err.Error(), "provider: "); response.Status != http.StatusBadRequest || response.Message != want {
			t.Errorf("translateProviderError(%v) = %d %q, want 400 %q", err, response.Status, response.Message, want)
		}
	}
}

func TestResponseOrKeepsAWordedAnswer(t *testing.T) {
	worded := httpx.BadRequest("no")
	fallbackRan := false
	otherwise := func(err error) error { fallbackRan = true; return httpx.Internal(err) }

	if got := responseOr(fmt.Errorf("inside: %w", worded), otherwise); got != error(worded) || fallbackRan {
		t.Errorf("a wrapped answer came back as %v (fallback ran: %v)", got, fallbackRan)
	}
	plain := errors.New("disk on fire")
	var response *httpx.Error
	if got := responseOr(plain, otherwise); !fallbackRan || !errors.As(got, &response) || response.Status != http.StatusInternalServerError {
		t.Errorf("a plain error came back as %v (fallback ran: %v)", got, fallbackRan)
	}
}

// The paging and window parameters fall back rather than refuse; see
// queryNumber. Atoi and ParseInt(…, 64) were the two spellings it replaced.
func TestQueryNumbersFallBackAsTheyDidBefore(t *testing.T) {
	samples := []string{
		"", "0", "7", "-1", "+5", "abc", "1.5", " 3", "9223372036854775807",
		"9223372036854775808", "-9223372036854775808", strconv.FormatInt(math.MaxInt32+1, 10),
	}
	for _, raw := range samples {
		wantInt := 42
		if raw != "" {
			if value, err := strconv.Atoi(raw); err == nil && value >= 0 {
				wantInt = value
			}
		}
		if got := intParam(raw, 42); got != wantInt {
			t.Errorf("intParam(%q) = %d, want %d", raw, got, wantInt)
		}

		wantInt64 := int64(42)
		if raw != "" {
			if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value >= 0 {
				wantInt64 = value
			}
		}
		if got := int64Param(raw, 42); got != wantInt64 {
			t.Errorf("int64Param(%q) = %d, want %d", raw, got, wantInt64)
		}
	}
}
