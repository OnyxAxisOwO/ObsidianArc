package feedback

import (
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Each refusal answers with the code its reader's screen translates. A plain
// sentence would be shown in English on a Chinese screen, and the length
// refusals carry their limit so the translation can quote it.
func TestFeedbackRefusalsCarryCodesAndTheirLimits(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"not found", ErrNotFound, "feedback_not_found"},
		{"title", ErrInvalidTitle, "feedback_title_invalid"},
		{"body", ErrInvalidBody, "feedback_body_invalid"},
		{"kind", ErrInvalidKind, "feedback_kind_invalid"},
		{"priority", ErrInvalidPrio, "feedback_priority_invalid"},
		{"status", ErrInvalidStatus, "feedback_status_invalid"},
		{"thread", ErrThreadFull, "feedback_thread_full"},
		{"daily limit", ErrTooMany, "feedback_daily_limit"},
	}
	for _, c := range cases {
		var apiErr *httpx.Error
		if !errors.As(TranslateError(c.err), &apiErr) {
			t.Fatalf("%s: TranslateError did not return an httpx error", c.name)
		}
		if apiErr.Code != c.code {
			t.Errorf("%s: code = %q, want %q", c.name, apiErr.Code, c.code)
		}
	}

	var title *httpx.Error
	errors.As(TranslateError(ErrInvalidTitle), &title)
	if title.Details["max"] != MaxTitleChars {
		t.Errorf("title details = %v, want the title limit %d", title.Details, MaxTitleChars)
	}
	var body *httpx.Error
	errors.As(TranslateError(ErrInvalidBody), &body)
	if body.Details["max"] != MaxBodyChars {
		t.Errorf("body details = %v, want the body limit %d", body.Details, MaxBodyChars)
	}
}
