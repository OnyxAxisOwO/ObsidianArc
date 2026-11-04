package turnstile

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// The two failures reach the client under their own codes and statuses: a
// refused token is the visitor's to retry, and an outage at Cloudflare is a
// 503 so a monitor can tell it apart from a wave of bots.
func TestHTTPErrorWordsBothChallengeFailures(t *testing.T) {
	failed := HTTPError(ErrFailed)
	if failed == nil || failed.Status != http.StatusForbidden || failed.Code != "challenge_failed" {
		t.Fatalf("a refused challenge = %+v, want 403 challenge_failed", failed)
	}

	unavailable := HTTPError(fmt.Errorf("reach cloudflare: %w", ErrUnavailable))
	if unavailable == nil || unavailable.Status != http.StatusServiceUnavailable || unavailable.Code != "challenge_unavailable" {
		t.Fatalf("an unreachable challenge = %+v, want 503 challenge_unavailable", unavailable)
	}
}

// Anything else is the caller's own fault to answer, so it must come back nil
// rather than as a challenge refusal.
func TestHTTPErrorLeavesOtherErrorsToTheCaller(t *testing.T) {
	if got := HTTPError(errors.New("database is down")); got != nil {
		t.Fatalf("an unrelated error was answered as a challenge: %+v", got)
	}
}
