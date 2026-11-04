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

// The newest reports the account page lists must reach the daily cap, or the
// count of reports left today reads high.
func TestTheListedNewestReachesTheDailyCap(t *testing.T) {
	if listedNewest < MaxPerDay {
		t.Fatalf("listedNewest = %d is below MaxPerDay = %d", listedNewest, MaxPerDay)
	}
}

// The kind, priority and status checks are a set lookup now, and the three
// sets are exactly what the constants name.
func TestValidValuesAreTheNamedOnes(t *testing.T) {
	for _, k := range []Kind{KindBug, KindIdea} {
		if !k.Valid() {
			t.Errorf("kind %q is not valid", k)
		}
	}
	if Kind("question").Valid() {
		t.Error("a kind outside the set is valid")
	}
	for _, p := range []Priority{PriorityLow, PriorityMedium, PriorityHigh} {
		if !p.Valid() {
			t.Errorf("priority %q is not valid", p)
		}
	}
	if Priority("urgent").Valid() {
		t.Error("a priority outside the set is valid")
	}
	for _, s := range []Status{StatusOpen, StatusResolved} {
		if !s.Valid() {
			t.Errorf("status %q is not valid", s)
		}
	}
	if Status("closed").Valid() {
		t.Error("a status outside the set is valid")
	}
}
