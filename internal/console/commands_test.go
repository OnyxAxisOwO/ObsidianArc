package console

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// adminRouteSource reads internal/admin/admin.go once per test run. It is
// the same file security_test.go's adminRoutes scans, with the same regex
// shape, so a route this package's commands claim and a route that test
// counts can never silently drift apart from two different ideas of what
// "the route table" means.
func adminRouteSource(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "admin", "admin.go"))
	if err != nil {
		t.Fatalf("read the admin route table: %v", err)
	}
	return string(source)
}

func adminRoutesFromSource(t *testing.T) []string {
	t.Helper()
	pattern := regexp.MustCompile(`mux\.Handle\("((?:GET|POST|PATCH|PUT|DELETE) /api/admin/[^"]*)"`)
	var out []string
	for _, match := range pattern.FindAllStringSubmatch(adminRouteSource(t), -1) {
		out = append(out, match[1])
	}
	if len(out) == 0 {
		t.Fatal("found no admin routes; the scanner has drifted from the source")
	}
	return out
}

// routePermissions maps each route to the permission string it is mounted
// with, by matching the same mux.Handle(...) line through to its
// protected("<permission>", ...) call.
func routePermissions(t *testing.T) map[string]string {
	t.Helper()
	pattern := regexp.MustCompile(`mux\.Handle\("((?:GET|POST|PATCH|PUT|DELETE) /api/admin/[^"]*)",\s*protected\("([^"]*)"`)
	out := map[string]string{}
	for _, match := range pattern.FindAllStringSubmatch(adminRouteSource(t), -1) {
		out[match[1]] = match[2]
	}
	if len(out) == 0 {
		t.Fatal("found no protected(...) admin routes; the scanner has drifted from the source")
	}
	return out
}

// TestEveryAdminRouteIsClaimedByACommand is the route-parity test the
// contract requires (§1.8.1): every mux.Handle("METHOD /path", ...) line in
// internal/admin/admin.go must be reachable from at least one console
// command's declared Endpoints. A route missing here is a capability the
// browser admin UI has and the console does not — the one gap this whole
// feature exists to close.
func TestEveryAdminRouteIsClaimedByACommand(t *testing.T) {
	claimed := map[string]bool{}
	for _, cmd := range allCommands {
		for _, ep := range cmd.Endpoints {
			claimed[ep] = true
		}
	}
	for _, route := range adminRoutesFromSource(t) {
		if !claimed[route] {
			t.Errorf("route %q is not claimed by any command's Endpoints", route)
		}
	}
}

// consoleExempt is every account-facing route no command reaches, and why.
//
// The terminal is in every account's menu, and its promise is that anything
// a person can set or change about their own account from the screens can be
// done from here too. These are the routes that are not a setting or a
// change: signing in and out, the OAuth hand-offs a browser has to follow,
// holding a conversation or drawing a picture, and moving files the terminal
// has no way to show or pick. A route belongs here only with a reason that
// is still true; anything else wants a command.
var consoleExempt = map[string]string{
	"POST /api/auth/login":                          "signing in — the terminal is already signed in",
	"POST /api/auth/two-factor":                     "the code step of signing in — the terminal is already signed in",
	"POST /api/profile/two-factor/backoffice/leave": "sent by the browser as the backoffice page closes; the web terminal's unlock lapses on its own",
	"POST /api/auth/logout":                         "signing out would end the session the terminal itself runs in",
	"POST /api/auth/register":                       "creating an account happens before there is a terminal",
	"GET /api/auth/pow-challenge":                   "fetching a proof-of-work challenge happens before there is a terminal",
	"POST /api/auth/verify":                         "followed from the link in the verification email",
	"GET /api/auth/oauth/start/{provider}":          "a browser redirect to the identity provider",
	"GET /api/auth/oauth/callback/{provider}":       "a browser redirect back from the identity provider",
	"GET /api/auth/oauth/signup":                    "part of signing up through an identity provider",
	"POST /api/auth/oauth/signup":                   "part of signing up through an identity provider",
	"GET /api/site":                                 "public instance details for the sign-in page, not a setting",
	"GET /api/site/logo":                            "the site logo image itself",
	"GET /api/site/login-background/{variant}":      "the login background image itself",
	"POST /api/chat":                                "holding a conversation — the chat is where that happens",
	"POST /api/images/generate":                     "drawing a picture — the terminal cannot show one",
	"POST /api/attachments":                         "uploading an image, which the terminal cannot pick",
	"GET /api/attachments/{id}":                     "downloading an image, which the terminal cannot show",
	"GET /api/preferences/wallpaper":                "the wallpaper image itself; pref wallpaper-clear removes it",
	"PUT /api/preferences/wallpaper":                "uploading a wallpaper image, which the terminal cannot pick",
	"GET /api/notifications":                        "the browser's notification feed",
	"GET /api/notifications/poll":                   "the browser's notification feed",
	"POST /api/notifications/read":                  "the browser's notification feed",
}

// TestEveryAccountRouteIsClaimedOrExempt is the account-side twin of the
// admin route-parity test above: a route an account's own screens call is
// either reachable from a command or listed in consoleExempt with a reason.
// A new setting added to the interface and not to the terminal fails here,
// which is the point — the two drifted apart once already.
func TestEveryAccountRouteIsClaimedOrExempt(t *testing.T) {
	claimed := map[string]bool{}
	for _, cmd := range allCommands {
		for _, ep := range cmd.Endpoints {
			claimed[ep] = true
		}
	}
	routes := userRoutesFromSource(t)
	for route := range routes {
		if claimed[route] && consoleExempt[route] != "" {
			t.Errorf("route %q is both claimed by a command and exempt; drop the exemption", route)
		}
		if !claimed[route] && consoleExempt[route] == "" {
			t.Errorf("route %q is not claimed by any command and has no exemption", route)
		}
	}
	for route := range consoleExempt {
		if !routes[route] {
			t.Errorf("exemption %q names a route no package mounts any more", route)
		}
	}
}

// TestCommandPermissionMatchesItsEndpoints is the permission-parity test
// (§1.8.2): a command's declared Permission must be copied verbatim from
// the permission string its endpoints are actually mounted with in
// admin.go, never re-derived. Session commands (help, clear, …) declare no
// Endpoints and are exempt — there is nothing in admin.go to compare them
// against.
func TestCommandPermissionMatchesItsEndpoints(t *testing.T) {
	perms := routePermissions(t)
	everyones := userRoutesFromSource(t)

	for _, cmd := range allCommands {
		for _, ep := range cmd.Endpoints {
			// A command every signed-in account may run is checked against
			// the other half of the API — the routes an account's own
			// screens call. The check is not weaker for it, it is the same
			// check against the right table, and the admin one below stays
			// exactly as strict as it was.
			if cmd.Permission == Anyone {
				if _, isAdmin := perms[ep]; isAdmin {
					t.Errorf("command %q is open to every account but declares the administrative endpoint %q",
						cmd.Name, ep)
					continue
				}
				if !everyones[ep] {
					t.Errorf("command %q declares endpoint %q, which no user-facing package mounts",
						cmd.Name, ep)
				}
				continue
			}

			want, ok := perms[ep]
			if !ok {
				t.Errorf("command %q declares endpoint %q, which is not in admin.go's route table", cmd.Name, ep)
				continue
			}
			if want != cmd.Permission {
				t.Errorf("command %q has Permission %q but its endpoint %q is mounted with %q",
					cmd.Name, cmd.Permission, ep, want)
			}
		}
	}
}

// userRoutesFromSource is the other route table: every non-admin API route
// the packages an account's own screens talk to actually mount.
//
// Scanned from source for the same reason the admin one is — a table
// written out by hand here is a second copy that drifts — and the list of
// packages is the list server.go mounts on the console's own mux. A command
// reaching a route no package in that list serves would answer 404 at
// runtime; this is where that is caught instead.
func userRoutesFromSource(t *testing.T) map[string]bool {
	t.Helper()
	packages := []string{"auth", "apikey", "chat", "quota", "usage", "card", "backup", "project", "feedback", "oauth", "notify", "invite", "bonus", "checkin"}
	pattern := regexp.MustCompile(`mux\.Handle(?:Func)?\("((?:GET|POST|PATCH|PUT|DELETE) /api/[^"]*)"`)

	out := map[string]bool{}
	for _, name := range packages {
		matches, err := filepath.Glob(filepath.Join("..", name, "*.go"))
		if err != nil {
			t.Fatalf("scan %s: %v", name, err)
		}
		for _, file := range matches {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			source, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
				if strings.HasPrefix(match[1], "GET /api/admin/") {
					continue
				}
				out[match[1]] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no user-facing routes; the scanner has drifted from the source")
	}
	return out
}

// TestEveryCommandHasBilingualHelp is §1.8.4: every command needs a
// non-empty EN and ZH summary, a usage line, and help <name> must render
// without error. The "at least two examples" bar from the contract's §THE
// BAR is checked only for API-backed commands (those with Endpoints) —
// cmd_session.go's own commands (clear, exit, …) predate this file and are
// a different, already-reviewed category this task was not asked to
// re-litigate.
func TestEveryCommandHasBilingualHelp(t *testing.T) {
	reg := newRegistry(nil)
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	s := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 100}

	for _, cmd := range allCommands {
		if cmd.Summary.EN == "" {
			t.Errorf("%s: empty EN summary", cmd.Name)
		}
		if cmd.Summary.ZH == "" {
			t.Errorf("%s: empty ZH summary", cmd.Name)
		}
		if cmd.Usage == "" {
			t.Errorf("%s: empty usage line", cmd.Name)
		}
		if len(cmd.Endpoints) > 0 && len(cmd.Examples) < 2 {
			t.Errorf("%s: needs at least 2 examples, has %d", cmd.Name, len(cmd.Examples))
		}

		got, ok := reg.lookup(cmd.Name)
		if !ok {
			t.Fatalf("%s: not found in the built registry", cmd.Name)
		}
		var buf bytes.Buffer
		renderCommandHelp(&buf, s, got, reg)
		if buf.Len() == 0 {
			t.Errorf("%s: help rendered nothing", cmd.Name)
		}
	}
}

// Every example is a line someone copies out of help and types, so each one has
// to get through the steps Execute gives a typed line: tokenise, resolve the
// command, parse its flags, and refuse a bare value that was meant for a flag.
// Nothing else reads an example back through the parser (the test above only
// counts them and renders help), which is how two examples that no longer
// parse shipped.
func TestEveryExampleParses(t *testing.T) {
	c := New(Options{})
	for _, cmd := range allCommands {
		for _, line := range cmd.Examples {
			if err := parseExample(c, line); err != nil {
				t.Errorf("%s: example %q does not parse: %v", cmd.Name, line, err)
			}
		}
	}
}

// parseExample takes one example through the parser without running it.
func parseExample(c *Console, line string) error {
	tokens, err := Tokenize(line)
	if err != nil {
		return err
	}
	return parseTokens(c, tokens)
}

// parseTokens follows a watch into the command it repeats, as runTokens does
// with the nested line, so the examples of the wrapped command are checked too.
func parseTokens(c *Console, tokens []string) error {
	if len(tokens) == 0 {
		return fmt.Errorf("no command given")
	}
	name, rest, found := c.match(tokens)
	if !found {
		return fmt.Errorf("no command named %q", name)
	}
	cmd, _ := c.reg.lookup(name)
	parsed, err := ParseFlags(rest, cmd.Flags)
	if err != nil {
		return err
	}
	if err := refuseLooseValues(cmd, parsed); err != nil {
		return err
	}
	if cmd.Name == "watch" {
		return parseTokens(c, parsed.Args)
	}
	return nil
}

// TestDestructiveCommandsRefuseWithoutYes is §1.8.5: every command marked
// Destructive must refuse before Run is ever called when --yes/-y is
// absent. The dispatcher below fails the test if a destructive command ever
// reaches it without confirmation — proof that Execute's own gate, not a
// well-behaved Run function, is what is actually being tested.
func TestDestructiveCommandsRefuseWithoutYes(t *testing.T) {
	c := New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
		t.Fatalf("dispatch called (%s %s) for a destructive command with no --yes", method, path)
		return Response{}, nil
	}})
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}

	var found int
	for _, cmd := range allCommands {
		if !cmd.Destructive {
			continue
		}
		found++
		s := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 100}
		var out bytes.Buffer
		result := c.Execute(context.Background(), s, &out, cmd.Name)
		if result.OK {
			t.Errorf("%s: succeeded without --yes", cmd.Name)
		}
		if result.Code != "confirmation_required" {
			t.Errorf("%s: want code confirmation_required, got %q (output: %q)", cmd.Name, result.Code, out.String())
		}
	}
	if found == 0 {
		t.Fatal("no destructive commands were registered; the scanner has drifted")
	}
}

// TestLimitedAdministratorSeesOnlyGrantedCommands is §1.8.8 together with
// the task's own fifth requirement: an administrator holding only "users"
// can run a users-domain command, is refused a command outside that grant,
// and never sees the refused commands in help or in the spec — hiding a
// command a caller cannot run is Execute's job just as much as refusing it
// is.
func TestLimitedAdministratorSeesOnlyGrantedCommands(t *testing.T) {
	c := New(Options{Dispatch: func(_ context.Context, _ user.User, _, _ string, _ any) (Response, error) {
		return Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
	}})
	actor := user.User{ID: id.New(), Username: "helpdesk", Role: user.RoleAdmin, AdminPermissions: []string{"users"}}
	newSession := func() *Session { return &Session{Actor: actor, Transport: "web", Lang: "en", Width: 100} }

	var out bytes.Buffer
	result := c.Execute(context.Background(), newSession(), &out, "user list")
	if !result.OK {
		t.Fatalf("user list should succeed for a users-only admin: %q", out.String())
	}

	forbidden := []string{"model list", "setting set", "group delete"}
	for _, name := range forbidden {
		out.Reset()
		result := c.Execute(context.Background(), newSession(), &out, name)
		if result.Code != "permission_denied" {
			t.Errorf("%s: want code permission_denied, got %q (output: %q)", name, result.Code, out.String())
		}
	}

	out.Reset()
	c.Execute(context.Background(), newSession(), &out, "help")
	helpText := out.String()
	for _, name := range forbidden {
		if strings.Contains(helpText, name) {
			t.Errorf("help output mentions %q, which this actor cannot run:\n%s", name, helpText)
		}
	}

	spec := c.Spec(newSession())
	for _, sc := range spec.Commands {
		for _, name := range forbidden {
			if sc.Name == name {
				t.Errorf("spec lists %q, which this actor cannot run", sc.Name)
			}
		}
	}
}
