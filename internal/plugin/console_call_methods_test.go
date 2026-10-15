package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The console decides what a command may do by what it declares: a read-only
// command is not refused the partial-name and watch --count guards that a
// writing one is. console.call must not be a way round that, so a backend
// whose command declared only reads cannot send a write through it.
func TestAPackageCallsOnlyTheMethodsItDeclares(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints []string
		method    string
		ok        bool
	}{
		{"a read from a read command", []string{"GET /api/admin/groups"}, "GET", true},
		{"a write from a read command", []string{"GET /api/admin/groups"}, "POST", false},
		{"a write from a write command", []string{"POST /api/admin/groups"}, "POST", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			var sent []string
			c := console.New(console.Options{
				Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (console.Response, error) {
					sent = append(sent, method+" "+path)
					return console.Response{Status: 200, Body: []byte(`{}`)}, nil
				},
			})
			cmds := (&Manager{}).commandsOf(&loaded{name: "demo", pkg: &arcx.Package{Manifest: &arcx.Manifest{
				Console: []arcx.ConsoleCommand{{
					Name: "demo call", Summary: arcx.Text{EN: "Call"},
					Permission: "super_admin", Endpoints: tc.endpoints,
				}},
			}}})

			host := r.manager.hostFunc(&loaded{name: "demo", pkg: &arcx.Package{
				Manifest: &arcx.Manifest{Permissions: []string{arcx.PermConsole}},
			}})
			raw := json.RawMessage(`{"method":"` + tc.method + `","path":"/api/admin/groups"}`)
			var refused error
			cmds[0].Run = func(ctx context.Context, rt *console.Runtime) error {
				_, refused = host(&wasm.Call{Ctx: ctx, State: &callState{console: rt}}, "console.call", raw)
				return nil
			}
			if err := c.AddCommands("demo", cmds); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			session := &console.Session{Actor: user.User{Role: user.RoleSuperAdmin}, Transport: "web", Lang: "en"}
			if result := c.Execute(context.Background(), session, &out, "demo call"); !result.OK {
				t.Fatalf("the command failed: %s", out.String())
			}

			if tc.ok {
				if refused != nil {
					t.Fatalf("the call was refused: %v", refused)
				}
				if len(sent) != 1 || sent[0] != tc.method+" /api/admin/groups" {
					t.Errorf("the admin API was sent %v, want one %s", sent, tc.method)
				}
				return
			}
			if hostCode(refused) != "bad_argument" {
				t.Fatalf("a %s from a read command: %v, want bad_argument", tc.method, refused)
			}
			if len(sent) != 0 {
				t.Errorf("the refused call still reached the admin API: %v", sent)
			}
		})
	}
}
