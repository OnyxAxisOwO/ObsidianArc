package console

import (
	"regexp"
	"strings"
	"testing"
)

// A flag's placeholder is what decides how its value is checked (see
// placeholderKind), and its Default is only what help prints. Neither is read
// by the Run that uses the flag, so nothing but this test notices when the two
// sides of a real command disagree: a placeholder renamed into text silently
// stops refusing "--limit abc", and a hint's "default 20" with no Default, or a
// different one, leaves help saying two things.
func TestRegisteredFlagsDeclareWhatTheirHintsSay(t *testing.T) {
	hintDefault := regexp.MustCompile(`\bdefault (-?[0-9][0-9.]*[a-z]*)\b`)
	for _, cmd := range allCommands {
		for _, f := range cmd.Flags {
			where := cmd.Name + " " + f.Name
			if f.Default != "" {
				if err := checkValue(f, f.Default); err != nil {
					t.Errorf("%s: Default %q is not a value the flag accepts: %v", where, f.Default, err)
				}
			}
			switch strings.TrimPrefix(f.Name, "--") {
			case "limit", "offset", "count", "port":
				if placeholderKind(f.Value) != kindInt {
					t.Errorf("%s: placeholder %q is not an integer placeholder, so the value goes unchecked", where, f.Value)
				}
			}
			m := hintDefault.FindStringSubmatch(f.Hint.EN)
			if m == nil {
				continue
			}
			// An edit form shares its create form's hints and leaves Default empty
			// on purpose: a flag left out of an edit changes nothing, so there is
			// no default to print.
			if f.Default == "" && strings.HasSuffix(cmd.Name, " edit") {
				continue
			}
			if f.Default != m[1] {
				t.Errorf("%s: hint says default %s, Default is %q", where, m[1], f.Default)
			}
		}
	}
}
