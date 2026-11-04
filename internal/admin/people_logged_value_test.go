package admin

import "testing"

type loggedTestRole string

// The audit line reads a request field as its value. A pointer logged as itself
// prints an address under the text handler and its value under the JSON one, so
// the same line would say different things depending on the handler.
func TestLoggedValueIsTheFieldsValueOrEmpty(t *testing.T) {
	role := loggedTestRole("admin")
	if got := loggedValue(&role); got != "admin" {
		t.Errorf("a set field is logged as %q, want its value", got)
	}
	if got := loggedValue[loggedTestRole](nil); got != "" {
		t.Errorf("an absent field is logged as %q, want empty", got)
	}
}
