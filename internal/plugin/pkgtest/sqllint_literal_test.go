package pkgtest

import (
	"strings"
	"testing"
)

// A "--" inside a quoted literal is text. Cutting the line there hid the
// ifnull that follows it from the runtime rules, so the statement passed the
// lint although the engine it was written for would not run it.
func TestADoubleDashInsideALiteralDoesNotHideTheRestOfTheStatement(t *testing.T) {
	problems := lintSource(t, "var q = \"SELECT '--', ifnull(x,0) FROM t\"\n"+
		"var r = \"SELECT 'it''s -- still text', ifnull(y,0) FROM t\"\n")
	for _, prefix := range []string{"main.go:3", "main.go:4"} {
		found := false
		for _, p := range problems {
			if strings.HasPrefix(p, prefix) {
				found = true
			}
		}
		if !found {
			t.Errorf("no problem reported at %s; got %v", prefix, problems)
		}
	}
}

// A real comment ends at its newline: what it says is not linted, and the
// statement's own text after it still is. The problem is reported at the line
// the statement starts on, so the one ifnull is the only thing expected.
func TestARealCommentStillEndsAtItsLine(t *testing.T) {
	problems := lintSource(t, "const a = `SELECT 1 FROM t -- ifnull(x, 0) 'unbalanced\n"+
		"WHERE ifnull(y, 0) = 1`\n")
	if len(problems) != 1 || !strings.Contains(problems[0], "ifnull") {
		t.Fatalf("want exactly the statement's own ifnull, got %v", problems)
	}
}
