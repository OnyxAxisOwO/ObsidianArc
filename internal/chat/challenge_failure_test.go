package chat

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
)

// Both gates answer a failed verification through challengeFailure, so the
// codes a browser branches on cannot differ between the chat door and the
// image door.
func TestChallengeFailureAnswersEachOutcomeTheSameAtEveryGate(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"rejected token", turnstile.ErrFailed, http.StatusForbidden, "challenge_failed"},
		{"verifier unreachable", turnstile.ErrUnavailable, http.StatusServiceUnavailable, "challenge_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var apiErr *httpx.Error
			if !errors.As(challengeFailure(tc.err), &apiErr) {
				t.Fatalf("challengeFailure(%v) is not an httpx error", tc.err)
			}
			if apiErr.Status != tc.status || apiErr.Code != tc.code {
				t.Errorf("got %d %s, want %d %s", apiErr.Status, apiErr.Code, tc.status, tc.code)
			}
		})
	}
}

// Anything that is not a verification outcome is this server's fault, and it
// must not be dressed up as a challenge the browser can retry.
func TestChallengeFailureDoesNotDressAServerFaultAsAChallenge(t *testing.T) {
	var apiErr *httpx.Error
	if !errors.As(challengeFailure(errors.New("database is gone")), &apiErr) {
		t.Fatal("an unknown failure is not an httpx error")
	}
	if apiErr.Status != http.StatusInternalServerError || apiErr.Code != "internal" {
		t.Errorf("got %d %s, want 500 internal", apiErr.Status, apiErr.Code)
	}
}

func TestClientIPIsEmptyWithoutAReader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := (&Handlers{}).clientIP(r); got != "" {
		t.Errorf("clientIP without a reader = %q, want empty", got)
	}
	h := &Handlers{ClientIP: func(*http.Request) string { return "203.0.113.7" }}
	if got := h.clientIP(r); got != "203.0.113.7" {
		t.Errorf("clientIP = %q, want the reader's answer", got)
	}
}
