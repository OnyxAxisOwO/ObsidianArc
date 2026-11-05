package console

import (
	"reflect"
	"testing"
)

func TestTokenizeSplitsOnWhitespace(t *testing.T) {
	got, err := Tokenize("user list --q alice")
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	want := []string{"user", "list", "--q", "alice"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTokenizeHonoursDoubleQuotes(t *testing.T) {
	got, err := Tokenize(`user list --q "alice smith"`)
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	want := []string{"user", "list", "--q", "alice smith"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTokenizeHonoursSingleQuotesLiterally(t *testing.T) {
	// Inside single quotes nothing is special, including a backslash —
	// exactly what a shell does, and the opposite of the double-quote case
	// below.
	got, err := Tokenize(`echo 'a\b "c"'`)
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	want := []string{"echo", `a\b "c"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTokenizeHandlesEscapesInsideAndOutsideQuotes(t *testing.T) {
	tests := []struct {
		name, line string
		want       []string
	}{
		{"escaped space outside quotes", `echo a\ b`, []string{"echo", "a b"}},
		{"escaped quote inside double quotes", `echo "a \"b\" c"`, []string{"echo", `a "b" c`}},
		{"escaped backslash inside double quotes", `echo "a\\b"`, []string{"echo", `a\b`}},
		{"lone trailing backslash is literal", `echo a\`, []string{"echo", `a\`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Tokenize(tt.line)
			if err != nil {
				t.Fatalf("tokenize %q: %v", tt.line, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tokenize %q = %#v, want %#v", tt.line, got, tt.want)
			}
		})
	}
}

func TestTokenizeReportsUnterminatedQuotes(t *testing.T) {
	if _, err := Tokenize(`echo "unterminated`); err == nil {
		t.Fatal("expected an error for an unterminated double quote")
	}
	if _, err := Tokenize(`echo 'unterminated`); err == nil {
		t.Fatal("expected an error for an unterminated single quote")
	}
}

func TestTokenizeEmptyAndBlankLines(t *testing.T) {
	for _, line := range []string{"", "   ", "\t \t"} {
		got, err := Tokenize(line)
		if err != nil {
			t.Fatalf("tokenize %q: %v", line, err)
		}
		if len(got) != 0 {
			t.Fatalf("tokenize %q = %#v, want no tokens", line, got)
		}
	}
}

func testFlags() []Flag {
	return []Flag{
		{Name: "--q", Value: "TEXT"},
		{Name: "--limit", Short: "-l", Value: "N"},
		{Name: "--hidden"}, // boolean, no Value
	}
}

func TestParseFlagsLongFormWithEquals(t *testing.T) {
	parsed, err := ParseFlags([]string{"--q=alice", "--limit=10"}, testFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Flags["q"] != "alice" || parsed.Flags["limit"] != "10" {
		t.Fatalf("got flags %#v", parsed.Flags)
	}
	if len(parsed.Args) != 0 {
		t.Fatalf("got args %#v, want none", parsed.Args)
	}
}

func TestParseFlagsLongFormWithSeparateValue(t *testing.T) {
	parsed, err := ParseFlags([]string{"--q", "alice", "--limit", "10"}, testFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Flags["q"] != "alice" || parsed.Flags["limit"] != "10" {
		t.Fatalf("got flags %#v", parsed.Flags)
	}
}

func TestParseFlagsBooleanFlagDoesNotConsumeTheNextToken(t *testing.T) {
	parsed, err := ParseFlags([]string{"--hidden", "some-arg"}, testFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Flags["hidden"] != "true" {
		t.Fatalf("got hidden=%q, want true", parsed.Flags["hidden"])
	}
	if want := []string{"some-arg"}; !reflect.DeepEqual(parsed.Args, want) {
		t.Fatalf("got args %#v, want %#v", parsed.Args, want)
	}
}

func TestParseFlagsDoubleDashStopsFlagParsing(t *testing.T) {
	parsed, err := ParseFlags([]string{"--limit", "10", "--", "--q", "not-a-flag"}, testFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Flags["limit"] != "10" {
		t.Fatalf("got limit=%q", parsed.Flags["limit"])
	}
	want := []string{"--q", "not-a-flag"}
	if !reflect.DeepEqual(parsed.Args, want) {
		t.Fatalf("got args %#v, want %#v (everything after -- is positional)", parsed.Args, want)
	}
}

func TestParseFlagsUnknownFlagIsAnError(t *testing.T) {
	if _, err := ParseFlags([]string{"--bogus"}, testFlags()); err == nil {
		t.Fatal("expected an error for an undeclared long flag")
	}
	if _, err := ParseFlags([]string{"-z"}, testFlags()); err == nil {
		t.Fatal("expected an error for an undeclared short flag")
	}
}

func TestParseFlagsMissingValueIsAnError(t *testing.T) {
	if _, err := ParseFlags([]string{"--q"}, testFlags()); err == nil {
		t.Fatal("expected an error when a value-taking flag has no value")
	}
}

func TestParseFlagsMixedPositionalAndFlags(t *testing.T) {
	parsed, err := ParseFlags([]string{"01ARZ3NDEKTSV4RRFFQ69G5FAV", "--limit", "5", "extra"}, testFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := []string{"01ARZ3NDEKTSV4RRFFQ69G5FAV", "extra"}; !reflect.DeepEqual(parsed.Args, want) {
		t.Fatalf("got args %#v, want %#v", parsed.Args, want)
	}
	if parsed.Flags["limit"] != "5" {
		t.Fatalf("got limit=%q", parsed.Flags["limit"])
	}
}
