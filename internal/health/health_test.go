package health

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func fail(at int64, code, source string) Sample {
	return Sample{At: at, OK: false, Code: code, Message: code + " happened", Source: source}
}

func pass(at int64, source string) Sample {
	return Sample{At: at, OK: true, Source: source}
}

// The run of failures that matters is the one that is still going. Samples
// arrive newest first, so a model that failed twice this hour after failing
// five times yesterday is two failures from being turned off, not seven.
func TestFailuresInARowCountFromTheNewestEnd(t *testing.T) {
	status := Summarise("m", []Sample{
		fail(500, "rate_limited", "user"),
		fail(400, "rate_limited", "user"),
		pass(300, "user"),
		fail(200, "timeout", "system"),
		fail(100, "timeout", "system"),
	})

	if status.FailuresInARow != 2 {
		t.Errorf("failures in a row = %d, want 2", status.FailuresInARow)
	}
	if status.State != StateDown {
		t.Errorf("state = %q, want down", status.State)
	}
	if status.Uptime != 0.2 {
		t.Errorf("uptime = %v, want 0.2", status.Uptime)
	}
	if status.LastOKAt != 300 || status.LastErrorAt != 500 {
		t.Errorf("last ok %d, last error %d", status.LastOKAt, status.LastErrorAt)
	}
	if status.LastCode != "rate_limited" {
		t.Errorf("last code = %q", status.LastCode)
	}
}

// Where the evidence came from is part of the answer: 100%% from one probe an
// hour is a different claim from 100%% across a thousand reader turns.
func TestTheTwoSourcesAreCountedApart(t *testing.T) {
	status := Summarise("m", []Sample{pass(300, "user"), pass(200, "system"), pass(100, "user")})

	if status.UserSamples != 2 || status.SystemSamples != 1 {
		t.Errorf("user %d, system %d, want 2 and 1", status.UserSamples, status.SystemSamples)
	}
	if status.State != StateUp || status.Uptime != 1 {
		t.Errorf("state %q uptime %v", status.State, status.Uptime)
	}
}

// The reason, ranked: an operator opening this wants the thing that is wrong
// most often at the top, not the thing that happened most recently.
func TestErrorsAreGroupedAndRanked(t *testing.T) {
	samples := []Sample{fail(900, "rate_limited", "user")}
	for i := range 4 {
		samples = append(samples, fail(int64(800-i), "unauthorized", "user"))
	}
	samples = append(samples, fail(100, "rate_limited", "user"))

	status := Summarise("m", samples)
	if len(status.Errors) != 2 {
		t.Fatalf("grouped into %d, want 2: %+v", len(status.Errors), status.Errors)
	}
	if status.Errors[0].Code != "unauthorized" || status.Errors[0].Count != 4 {
		t.Errorf("first = %+v, want unauthorized x4", status.Errors[0])
	}
	if status.Errors[1].Code != "rate_limited" || status.Errors[1].Count != 2 {
		t.Errorf("second = %+v, want rate_limited x2", status.Errors[1])
	}
	// The newest message for a code, because upstreams reword theirs.
	if status.Errors[1].LastAt != 900 {
		t.Errorf("last at = %d, want the newer of the two", status.Errors[1].LastAt)
	}
}

// Nothing is not zero. A model nobody has used and nothing has probed is
// unknown, and reporting 0%% would have an operator chasing a model that is
// probably fine.
func TestNoEvidenceIsUnknownAndNotZero(t *testing.T) {
	status := Summarise("m", nil)
	if status.State != StateUnknown {
		t.Errorf("state = %q, want unknown", status.State)
	}
	if status.Samples != 0 || status.Uptime != 0 || status.FailuresInARow != 0 {
		t.Errorf("%+v", status)
	}
	if len(status.Errors) != 0 {
		t.Errorf("errors = %+v", status.Errors)
	}
}

// The codes are what the page groups by, so the mapping is the difference
// between one line saying "the key is wrong" and forty saying nothing.
func TestFailuresAreGroupedIntoCodesAnOperatorCanActrOn(t *testing.T) {
	cases := map[string]string{
		"http 401 unauthorized":                     "unauthorized",
		"provider returned 429 rate limit exceeded": "rate_limited",
		"model not found: 404":                      "model_not_found",
		"dial tcp 10.0.0.1:443: connection refused": "unreachable",
		"upstream 503 service unavailable":          "upstream_error",
		"something nobody has seen before":          "error",
	}
	for message, want := range cases {
		if got := Code(errors.New(message)); got != want {
			t.Errorf("Code(%q) = %q, want %q", message, got, want)
		}
	}
	if got := Code(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("a deadline gave %q", got)
	}
	if got := Code(nil); got != "" {
		t.Errorf("no error gave %q", got)
	}
	// Wrapped, because that is how it will actually arrive.
	wrapped := fmt.Errorf("chat: %w", context.DeadlineExceeded)
	if got := Code(wrapped); got != "timeout" {
		t.Errorf("a wrapped deadline gave %q", got)
	}
}
