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
// writing one is, and a command not marked destructive is not asked for --yes.
// console.call must not be a way round that, so a backend reaches the
// endpoints its command declared, by method and path, and no others.
func TestAPackageCallsOnlyTheEndpointsItDeclares(t *testing.T) {
	runConsoleCalls(t, arcx.APILevel, []consoleCallCase{
		{"a read from a read command", []string{"GET /api/admin/groups"}, "GET", "/api/admin/groups", true},
		{"a write from a read command", []string{"GET /api/admin/groups"}, "POST", "/api/admin/groups", false},
		{"a write from a write command", []string{"POST /api/admin/groups"}, "POST", "/api/admin/groups", true},
		{"a delete from a command that declared a create", []string{"POST /api/admin/groups"}, "DELETE", "/api/admin/groups", false},
		{"a write elsewhere from a write command", []string{"POST /api/admin/groups"}, "POST", "/api/admin/users", false},
		{"a read with a query string", []string{"GET /api/admin/groups"}, "GET", "/api/admin/groups?limit=5", true},
		{"a path parameter", []string{"PATCH /api/admin/groups/{id}"}, "PATCH", "/api/admin/groups/01ABC", true},
		{"a path parameter that climbs out", []string{"PATCH /api/admin/groups/{id}/x"}, "PATCH", "/api/admin/groups/../x", false},
		{"a path one segment longer", []string{"PATCH /api/admin/groups/{id}"}, "PATCH", "/api/admin/groups/01ABC/members", false},
	})
}

// A package built before the endpoints were enforced was written against the
// method-only rule, and an upgrade of the server must not start refusing the
// calls it already makes: a write still needs a command that declares one, but
// it is not held to the path.
func TestAnOlderPackageKeepsTheMethodOnlyRule(t *testing.T) {
	runConsoleCalls(t, arcx.DeclaredEndpointsAPI-1, []consoleCallCase{
		{"a read from a read command", []string{"GET /api/admin/groups"}, "GET", "/api/admin/groups", true},
		{"a read it did not declare", []string{"GET /api/admin/groups"}, "GET", "/api/admin/users", true},
		{"a write from a read command", []string{"GET /api/admin/groups"}, "POST", "/api/admin/groups", false},
		{"a write elsewhere from a write command", []string{"POST /api/admin/groups"}, "POST", "/api/admin/users/01ABC", true},
		{"outside the backoffice", []string{"POST /api/admin/groups"}, "POST", "/api/auth/logout", false},
	})
}

type consoleCallCase struct {
	name      string
	endpoints []string
	method    string
	path      string
	ok        bool
}

func runConsoleCalls(t *testing.T, api int, cases []consoleCallCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			var sent []string
			c := console.New(console.Options{
				Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (console.Response, error) {
					sent = append(sent, method+" "+path)
					return console.Response{Status: 200, Body: []byte(`{}`)}, nil
				},
			})
			man := &arcx.Manifest{
				Requires:    arcx.Requires{API: api},
				Permissions: []string{arcx.PermConsole},
				Console: []arcx.ConsoleCommand{{
					Name: "demo call", Summary: arcx.Text{EN: "Call"},
					Permission: "super_admin", Endpoints: tc.endpoints,
				}},
			}
			cmds := (&Manager{}).commandsOf(&loaded{name: "demo", pkg: &arcx.Package{Manifest: man}})
			host := r.manager.hostFunc(&loaded{name: "demo", pkg: &arcx.Package{Manifest: man}})
			raw, err := json.Marshal(map[string]string{"method": tc.method, "path": tc.path})
			if err != nil {
				t.Fatal(err)
			}
			var refused error
			cmds[0].Run = func(ctx context.Context, rt *console.Runtime) error {
				_, refused = host(&wasm.Call{Ctx: ctx, State: commandState(rt, man, man.Console[0])}, "console.call", raw)
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
				if len(sent) != 1 || sent[0] != tc.method+" "+tc.path {
					t.Errorf("the admin API was sent %v, want one %s", sent, tc.method)
				}
				return
			}
			if hostCode(refused) != "bad_argument" {
				t.Fatalf("%s %s: %v, want bad_argument", tc.method, tc.path, refused)
			}
			if len(sent) != 0 {
				t.Errorf("the refused call still reached the admin API: %v", sent)
			}
		})
	}
}
