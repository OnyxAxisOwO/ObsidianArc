package plugin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A destructive command refuses positionals beyond the ones it declares, unless
// its last argument reads the rest of the line. A package has to be able to say
// that, or its backend, which is handed every token, would be refused a line it
// was written to take.
func TestAPackageCommandReadsTheRestOfItsLineOnlyWhenItSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name string
		rest bool
		ok   bool
	}{
		{"declared as rest", true, true},
		{"not declared as rest", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmds := (&Manager{}).commandsOf(&loaded{name: "demo", pkg: &arcx.Package{Manifest: &arcx.Manifest{
				Console: []arcx.ConsoleCommand{{
					Name: "demo import", Summary: arcx.Text{EN: "Import"},
					Permission: "super_admin", Destructive: true,
					Args: []arcx.ConsoleArg{{Name: "json", Required: true, Rest: tc.rest}},
				}},
			}}})
			var handed []string
			cmds[0].Run = func(_ context.Context, rt *console.Runtime) error {
				handed = rt.Args()
				return nil
			}
			c := console.New(console.Options{})
			if err := c.AddCommands("demo", cmds); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			session := &console.Session{Actor: user.User{Role: user.RoleSuperAdmin}, Transport: "web", Lang: "en"}
			result := c.Execute(context.Background(), session, &out, "demo import [1, 2] --yes")

			if result.OK != tc.ok {
				t.Fatalf("OK = %v, want %v; the line drew:\n%s", result.OK, tc.ok, out.String())
			}
			if tc.ok && len(handed) != 2 {
				t.Errorf("the backend was handed %v, want both tokens of the document", handed)
			}
			if !tc.ok && !strings.Contains(out.String(), "takes 1 argument") {
				t.Errorf("the refusal does not say why: %q", out.String())
			}
		})
	}
}
