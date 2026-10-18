package compat

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
)

// A key restricted to one model is restricted even when only ModelID is set,
// which is the state the fallback in keyModelIDs exists for.
func TestKeyWithOnlyASingleModelStaysLimitedToIt(t *testing.T) {
	if got := keyModelIDs(apikey.Key{ModelID: "gpt-x"}); len(got) != 1 || got[0] != "gpt-x" {
		t.Errorf("a key with only ModelID set = %v, want it limited to gpt-x", got)
	}
	if got := keyModelIDs(apikey.Key{}); got != nil {
		t.Errorf("a key with no restriction = %v, want nil (any model)", got)
	}
	got := keyModelIDs(apikey.Key{ModelID: "a", ModelIDs: []string{"b", "c"}})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("the list wins over ModelID: got %v, want [b c]", got)
	}
}
