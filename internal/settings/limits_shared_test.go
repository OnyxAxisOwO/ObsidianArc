package settings

import (
	"encoding/json"
	"os"
	"testing"
)

// The backoffice repeats MaxValueBytes as SETTING_MAX_BYTES, and both copies
// are held to one file: web/test/settings-draft.test.ts reads it too. Moving
// the limit here fails this test until the file follows, and the file then
// fails the browser's test until its copy does.
func TestMaxValueBytesMatchesTheSharedLimits(t *testing.T) {
	raw, err := os.ReadFile("testdata/limits.json")
	if err != nil {
		t.Fatal(err)
	}
	var limits struct {
		MaxValueBytes int `json:"max_value_bytes"`
	}
	if err := json.Unmarshal(raw, &limits); err != nil {
		t.Fatal(err)
	}
	if limits.MaxValueBytes != MaxValueBytes {
		t.Errorf("MaxValueBytes is %d, testdata/limits.json says %d", MaxValueBytes, limits.MaxValueBytes)
	}
}
