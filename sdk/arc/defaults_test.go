package arc

import (
	"encoding/json"
	"testing"
)

// A NULL reads as the zero value of each type a plugin scans into, and a type
// with no zero value refuses the NULL instead of being left as it was.
func TestANullScansAsTheZeroValueOfEachType(t *testing.T) {
	reset(t)
	useHost(t, map[string]any{
		"db.query": map[string]any{"columns": []string{"v"}, "rows": [][]any{{nil}}},
	})
	s, n, n64, flag, f, raw, v := "x", 7, int64(7), true, 1.5, []byte("x"), any("x")
	for _, dest := range []any{&s, &n, &n64, &flag, &f, &raw, &v} {
		if err := (&Ctx{}).QueryRow("SELECT v").Scan(dest); err != nil {
			t.Fatalf("NULL into %T: %v", dest, err)
		}
	}
	if s != "" || n != 0 || n64 != 0 || flag || f != 0 || raw != nil || v != nil {
		t.Fatalf("after NULL: %q %d %d %v %v %v %v", s, n, n64, flag, f, raw, v)
	}

	var unsupported map[string]int
	if err := (&Ctx{}).QueryRow("SELECT v").Scan(&unsupported); err == nil {
		t.Fatal("a NULL was scanned into a map")
	}
}

// The two integer destinations share one conversion, so both read the same
// number; the int branch is the one a plugin's int field reaches.
func TestAnIntegerScansIntoEitherWidth(t *testing.T) {
	reset(t)
	useHost(t, map[string]any{
		"db.query": map[string]any{"columns": []string{"v"}, "rows": [][]any{{json.Number("42")}}},
	})
	var asInt int
	var asInt64 int64
	if err := (&Ctx{}).QueryRow("SELECT v").Scan(&asInt); err != nil || asInt != 42 {
		t.Fatalf("into int: %d, %v", asInt, err)
	}
	if err := (&Ctx{}).QueryRow("SELECT v").Scan(&asInt64); err != nil || asInt64 != 42 {
		t.Fatalf("into int64: %d, %v", asInt64, err)
	}
}

// A kind with no handler registered answers by what it can safely say: a sweep
// and a description have such an answer, so they succeed, while a guard and an
// invitee decorator answer with an internal error that refuses.
func TestAKindWithNoHandlerAnswersByWhatItsKindCanSafelySay(t *testing.T) {
	reset(t)
	oldSweep := sweep
	sweep = nil
	t.Cleanup(func() { sweep = oldSweep })

	if _, e := serve(t, "sweep", nil); e != nil {
		t.Fatalf("a sweep with no handler: %v", e)
	}
	if res, e := serve(t, "describe", nil); e != nil || len(res) == 0 {
		t.Fatalf("a description with no handler: %s, %v", res, e)
	}
	if _, e := serve(t, "guard", map[string]any{"name": "missing"}); e == nil || e.Status != 500 || e.Code != "no_handler" {
		t.Fatalf("a guard with no handler: %+v", e)
	}
	if _, e := serve(t, "decorate_invitees", map[string]any{"inviter_id": "u", "invitees": []any{}}); e == nil || e.Status != 500 || e.Code != "no_handler" {
		t.Fatalf("a decorator with no handler: %+v", e)
	}
}
