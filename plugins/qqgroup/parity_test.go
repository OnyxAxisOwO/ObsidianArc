package qqgroup

import (
	"os"
	"regexp"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The backoffice page sends every key this plugin's browser half declares in
// the same PUT as its own, and the server refuses the whole request on the
// first key it does not know — so a key declared there and not defined here
// breaks the entire page, not just this plugin's card. The two lists are in
// different languages in different directories; only a test reading both
// can hold them together.
func TestEverySettingTheBrowserDeclaresIsDefined(t *testing.T) {
	source, err := os.ReadFile("../../web/src/plugins/qqgroup/qqgroup.plugin.ts")
	if err != nil {
		t.Fatalf("read the browser half: %v", err)
	}
	matches := regexp.MustCompile(`key: '([a-z][a-z0-9_]*\.[a-z][a-z0-9_.]*)'`).FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("found no setting keys; the scanner has drifted from the source")
	}
	for _, match := range matches {
		if d, ok := settings.Lookup(match[1]); !ok || d.Plugin != Name {
			t.Errorf("the browser half sends %q, which this plugin does not define", match[1])
		}
	}
}
