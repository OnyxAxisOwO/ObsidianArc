package console

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderTableAlignsColumnsWhenEverythingFits(t *testing.T) {
	var buf bytes.Buffer
	RenderTable(&buf, 100, false, []string{"id", "name"}, [][]string{
		{"1", "alice"},
		{"22", "bob"},
	})
	got := buf.String()
	want := "id  name \n1   alice\n22  bob  \n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

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

func TestRenderTableNeverShrinksBelowTheFloor(t *testing.T) {
	// Pathologically narrow: RenderTable must not panic or produce a
	// negative-width slice, and must still emit one row per input row.
	var buf bytes.Buffer
	RenderTable(&buf, 20, false, []string{"a", "b", "c", "d", "e"}, [][]string{
		{"111111", "222222", "333333", "444444", "555555"},
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (header + 1 row):\n%s", len(lines), buf.String())
	}
}

func TestRenderFieldsAlignsKeys(t *testing.T) {
	var buf bytes.Buffer
	RenderFields(&buf, false, [][2]string{
		{"username", "arc"},
		{"role", "admin"},
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "username: arc") {
		t.Fatalf("got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "role:") || !strings.HasSuffix(lines[1], "admin") {
		t.Fatalf("got %q", lines[1])
	}
	// The values are the value column, so they must start at the same
	// offset regardless of how long each key is.
	valueOffset := func(line, value string) int { return strings.Index(line, value) }
	if valueOffset(lines[0], "arc") != valueOffset(lines[1], "admin") {
		t.Fatalf("values are not aligned:\n%q\n%q", lines[0], lines[1])
	}
}

func TestRenderJSONPrettyPrintsTheRawPayloadVerbatim(t *testing.T) {
	var buf bytes.Buffer
	raw := []byte(`{"users":[{"id":"1"}],"total":1}`)
	if err := RenderJSON(&buf, raw); err != nil {
		t.Fatalf("render: %v", err)
	}

	var got, want any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("RenderJSON changed the payload: got %s, want %s", gotJSON, wantJSON)
	}
	if !strings.Contains(buf.String(), "\n") {
		t.Fatal("expected RenderJSON to pretty-print (indent) rather than emit one line")
	}
}

func TestRuntimeTableUsesJSONModeInsteadOfAGrid(t *testing.T) {
	rt := &Runtime{Session: &Session{JSON: true}, Out: &bytes.Buffer{}}
	buf := rt.Out.(*bytes.Buffer)

	if err := rt.Table([]string{"id", "name"}, [][]string{{"1", "alice"}}); err != nil {
		t.Fatalf("table: %v", err)
	}
	var decoded []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("expected JSON array output, got %q: %v", buf.String(), err)
	}
	if len(decoded) != 1 || decoded[0]["id"] != "1" || decoded[0]["name"] != "alice" {
		t.Fatalf("got %#v", decoded)
	}
	if strings.Contains(buf.String(), "id  name") {
		t.Fatal("expected no table grid while Session.JSON is set")
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

func TestRenderErrorReturnsTheCallErrorCodeAndShowsItDim(t *testing.T) {
	err := &CallError{Status: 403, Code: "admin_permission_denied", Message: "no."}
	var buf bytes.Buffer
	code := RenderError(&buf, false, false, err)
	if code != "admin_permission_denied" {
		t.Fatalf("got code %q", code)
	}
	if got := buf.String(); !strings.Contains(got, "error: no.") || !strings.Contains(got, "admin_permission_denied") {
		t.Fatalf("got %q", got)
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
