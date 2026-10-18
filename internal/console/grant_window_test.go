package console

import (
	"encoding/json"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// An empty grant list must reach the server as [] and never as null: the server
// reads null as "not given", so a clear would report success and keep every grant.
func TestAnEmptyGrantListIsNeverNil(t *testing.T) {
	for name, parse := range map[string]func(*Runtime, string) ([]map[string]any, error){
		"model grants": parseGrants,
		"group grants": parseGroupGrants,
	} {
		got, err := parse(nil, "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got == nil {
			t.Errorf("%s: an empty value came back nil, which marshals as null", name)
		}
		body, _ := json.Marshal(got)
		if string(body) != "[]" {
			t.Errorf("%s: an empty value marshals as %s, want []", name, body)
		}
	}
}

// Each entry is name:access, with access defaulting to use. An id is taken as it
// stands, so the parse needs no runtime.
func TestGrantEntriesCarryTheirAccess(t *testing.T) {
	first, second := id.New(), id.New()
	got, err := parseGrantList(nil, first+":view, "+second, "model_id", resolveModelRef)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"model_id": first, "access": "view"},
		{"model_id": second, "access": "use"},
	}
	body, _ := json.Marshal(got)
	wantBody, _ := json.Marshal(want)
	if string(body) != string(wantBody) {
		t.Errorf("got %s, want %s", body, wantBody)
	}
}

// The window ratio is the tightest used/limit across the limited dimensions, and
// the figure caps it at 100% while the ratio the credit listing sorts by does not.
func TestWindowRatioAndFigureReadTheTightestLimit(t *testing.T) {
	window := map[string]any{
		"used_requests": 50.0, "limit_requests": 100.0,
		"used_tokens": 900.0, "limit_tokens": 1000.0,
		"used_credits": 1.0, "limit_credits": nil,
	}
	ratio, has := windowRatio(window)
	if !has || ratio != 0.9 {
		t.Errorf("windowRatio = %v, %v; want 0.9, true", ratio, has)
	}
	if got := windowFigure(window); got != "90% used" {
		t.Errorf("windowFigure = %q, want %q", got, "90% used")
	}

	over := map[string]any{"used_requests": 150.0, "limit_requests": 100.0}
	if r, _ := windowRatio(over); r != 1.5 {
		t.Errorf("an over-limit window's ratio is %v, want it uncapped at 1.5", r)
	}
	if got := windowFigure(over); got != "100% used" {
		t.Errorf("an over-limit window shows %q, want %q", got, "100% used")
	}

	unlimited := map[string]any{"used_requests": 5.0, "limit_requests": nil}
	if _, has := windowRatio(unlimited); has {
		t.Error("a window with no limit reported a ratio")
	}
	if got := windowFigure(unlimited); got != "-" {
		t.Errorf("an unlimited window shows %q, want %q", got, "-")
	}
}
