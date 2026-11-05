package console

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderTableClipsAndTruncatesWithAnEllipsis(t *testing.T) {
	var buf bytes.Buffer
	// One header ("description") is far wider than the narrow width below
	// forces it to stay, so it must shrink and its long cell must be
	// truncated with an ellipsis rather than pushed past the width.
	RenderTable(&buf, 24, false, []string{"id", "description"}, [][]string{
		{"1", "a very long description that will not fit"},
	})
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), got)
	}
	for i, line := range lines {
		if runeLen := len([]rune(line)); runeLen > 24 {
			t.Errorf("line %d is %d runes wide, want <= 24: %q", i, runeLen, line)
		}
	}
	if !strings.Contains(lines[1], "…") {
		t.Fatalf("expected the truncated data row to contain an ellipsis, got %q", lines[1])
	}
	if strings.Contains(lines[1], "that will not fit") {
		t.Fatalf("expected the long cell to be truncated, got %q", lines[1])
	}
}

func TestRuntimeTablePrefersTheRawServerPayloadOverSyntheticJSON(t *testing.T) {
	rt := &Runtime{Session: &Session{JSON: true}, Out: &bytes.Buffer{}}
	rt.rawJSON = []byte(`{"users":[{"id":"1","username":"alice"}],"total":1}`)
	buf := rt.Out.(*bytes.Buffer)

	if err := rt.Table([]string{"id", "username"}, [][]string{{"1", "alice"}}); err != nil {
		t.Fatalf("table: %v", err)
	}
	if !strings.Contains(buf.String(), `"total": 1`) {
		t.Fatalf("expected the raw server payload (with \"total\") to be printed verbatim, got %q", buf.String())
	}
}

func TestRenderErrorJSONModeProducesAnErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	RenderError(&buf, false, true, &CallError{Status: 500, Message: "boom"})

	var decoded struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", buf.String(), err)
	}
	if decoded.Error.Message != "boom" {
		t.Fatalf("got message %q", decoded.Error.Message)
	}
}
