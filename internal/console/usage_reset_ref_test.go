package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// usage reset --scope user with a blank account ref resolves "" against the
// account list. Every field contains "", so a missing --id used to act on the
// only account on the instance. The reset is refused before it is sent.
func TestUsageResetWithoutAnAccountNeverResetsTheOnlyOne(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200,
			`{"users":[{"id":"` + id.New() + `","username":"alice"}],"total":1}`},
		flagRoute{"POST", "/api/admin/usage/reset", 200, `{"ok":true}`},
	)
	for _, line := range []string{
		"usage reset --scope user --yes",
		"usage reset --scope user --id '' --yes",
	} {
		result, out := rig.run(line)
		if result.OK {
			t.Errorf("%s was accepted: %s", line, out)
		}
		for _, call := range rig.calls {
			if strings.HasPrefix(call, "POST /api/admin/usage/reset") {
				t.Errorf("%s reached the server: %v", line, rig.calls)
			}
		}
	}
}
