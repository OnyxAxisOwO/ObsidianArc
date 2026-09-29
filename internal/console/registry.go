package console

// Adding a command is one call to registerCommand from an init function in
// its own cmd_*.go file. init runs before anything else in the program, so
// by the time New builds a Console every cmd_*.go in the binary has already
// added its commands to the shared, package-level list — no file has to
// import or even know about another one. This is the same pattern
// database/sql drivers and image formats use to register themselves.
//
// A complete, worked example — everything a command needs, and nothing it
// does not:
//
//	func init() {
//		registerCommand(Command{
//			Name:       "user list",
//			Group:      "accounts",
//			Summary:    Text{EN: "List accounts", ZH: "列出账户"},
//			Usage:      "user list [--q TEXT] [--role ROLE] [--limit N]",
//			Help: Text{
//				EN: "Lists accounts, newest first. --q matches username, email and nickname.",
//				ZH: "按创建时间倒序列出账户。--q 会匹配用户名、邮箱和昵称。",
//			},
//			Flags: []Flag{
//				{Name: "--q", Hint: Text{EN: "search text", ZH: "搜索关键字"}, Value: "TEXT"},
//				{Name: "--role", Hint: Text{EN: "user, admin or super_admin", ZH: "user、admin 或 super_admin"}, Value: "ROLE"},
//				{Name: "--limit", Hint: Text{EN: "page size, default 50", ZH: "每页数量，默认 50"}, Value: "N"},
//			},
//			Examples:   []string{"user list", "user list --q alice --role admin"},
//			Permission: "users",
//			Endpoints:  []string{"GET /api/admin/users"},
//			Run: func(ctx context.Context, rt *Runtime) error {
//				q := url.Values{}
//				if v := rt.String("q"); v != "" {
//					q.Set("q", v)
//				}
//				if v := rt.String("role"); v != "" {
//					q.Set("role", v)
//				}
//				q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
//
//				data, _, err := rt.Call(http.MethodGet, "/api/admin/users?"+q.Encode(), nil)
//				if err != nil {
//					return err
//				}
//				list, _ := data.(map[string]any)
//				users, _ := list["users"].([]any)
//
//				rows := make([][]string, 0, len(users))
//				for _, raw := range users {
//					u, _ := raw.(map[string]any)
//					rows = append(rows, []string{
//						fmt.Sprint(u["id"]), fmt.Sprint(u["username"]),
//						fmt.Sprint(u["role"]), fmt.Sprint(u["status"]),
//					})
//				}
//				return rt.Table([]string{"id", "username", "role", "status"}, rows)
//			},
//		})
//	}
//
// Three things worth pointing out in that example:
//
//   - Run never touches http.ResponseWriter or the admin handlers directly.
//     Call is the only door to the API, and it goes through the exact same
//     auth.RequireAdmin + per-route permission check a browser request would
//     — see dispatch.go.
//   - Table (and Fields, for a "show" command) already know how to answer
//     --json and the session's `format json`: when either is set they print
//     the server's own response body instead of building a table, so a
//     command almost never needs its own JSON branch.
//   - Permission is copied from the route table in API.md, verbatim — the
//     engine's own comma-list check (hasPermission, below) is only the
//     friendly refusal; the dispatched request is re-checked by the real
//     admin.Handlers.Routes wrapper regardless.
//
// A destructive command additionally sets Destructive: true and checks
// rt.Confirmed() is unnecessary in the common case — Execute already refuses
// to call Run at all until --yes (or -y) is present. Confirmed exists for a
// command that wants to branch on it anyway (a dry-run summary before the
// real call, say).
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Text is a string that exists in both languages this project ships. English
// is the fallback for a Chinese caller when ZH was left empty, and vice
// versa, so a half-translated Text still renders something rather than
// nothing.
type Text struct {
	EN string
	ZH string
}

// For resolves the string for lang ("en" or "zh"; anything else behaves like
// "en"). Command names, flag names and table headers are never wrapped in
// Text — they are protocol, like `reasoning_effort` elsewhere in this
// project, and stay English in both languages.
func (t Text) For(lang string) string {
	if lang == "zh" {
		if t.ZH != "" {
			return t.ZH
		}
		return t.EN
	}
	if t.EN != "" {
		return t.EN
	}
	return t.ZH
}

// Arg is one positional argument a command's Usage documents.
type Arg struct {
	Name     string
	Hint     Text
	Required bool
}

// Flag is one named flag. Value is the placeholder shown in help and the
// spec ("TEXT", "N", "DURATION"); an empty Value means the flag is boolean —
// its presence alone is the signal, as with --hidden or --enabled.
//
// A command must not declare "-h", "--help", "--json", "-y" or "--yes": the
// engine recognises all five on every command already (see parse.go and
// Execute), and a command-declared flag with one of those names would never
// be reached.
type Flag struct {
	Name    string
	Short   string
	Hint    Text
	Value   string
	Default string
	// Sensitive flags have their value replaced with *** in the audit
	// trail — a password or a generated secret should never sit in the
	// security_events table in plain text just because an administrator
	// typed it at a prompt instead of a form.
	Sensitive bool
}

// Command is one flat-named console command ("user list", "group delete").
// Register it with registerCommand from an init function; see the package
// comment above for a complete example.
type Command struct {
	// Name is the whole flat command, noun first: "user list", not
	// "list user". A single word ("help", "clear") is its own noun.
	Name string
	// Group is a plain slug ("accounts", "catalogue") that organises the
	// `help` index and the spec by noun-family rather than by the flat
	// registration order. help.go maps it to a bilingual heading.
	Group       string
	Summary     Text
	Usage       string
	Help        Text
	Args        []Arg
	Flags       []Flag
	Examples    []string
	SeeAlso     []string
	Permission  string
	Destructive bool
	// Endpoints are the admin routes this command calls, "METHOD /path" —
	// what help prints under "Calls:" and what a route-parity test (added
	// once every noun's commands exist) checks against admin.Routes.
	Endpoints []string
	Run       func(ctx context.Context, rt *Runtime) error
	// The plugin that registered it. A switched-off plugin's commands are
	// not found, not listed and not completed — the same as a build without
	// it.
	Plugin string
}

// allCommands is filled by every cmd_*.go file's init(), before New is ever
// called from anywhere in the program.
var allCommands []Command

func registerCommand(cmd Command) {
	if cmd.Name == "" {
		panic("console: a command was registered with no Name")
	}
	if cmd.Run == nil {
		panic("console: command " + cmd.Name + " was registered with no Run")
	}
	allCommands = append(allCommands, cmd)
}

// Register is registerCommand for a plugin, which lives outside this package
// and adds its commands from its own init the same way a cmd_*.go file does.
// Commands for a plugin's routes belong with the plugin: the route-parity
// test here reads admin.go alone, and a plugin's routes are not in it.
func Register(cmd Command) { registerCommand(cmd) }

// ResolveUser is how a plugin's command reads an account argument: an id as
// it stands, or a username looked up through the same admin list the core
// commands use, with the same "no such user" and "which one" answers.
func ResolveUser(rt *Runtime, ref string) (string, error) { return resolveUserRef(rt, ref) }

// RequireArg is the first positional argument, or the error the core
// commands give when it is missing; what names it for that message.
func RequireArg(rt *Runtime, what string) (string, error) { return requireRef(rt, what) }

// registry is the engine's own view of allCommands: a lookup by flat name,
// plus the registration order help and completion iterate in.
type registry struct {
	commands map[string]*Command
	order    []string
	gate     plugingate.Gate
}

func newRegistry(gate plugingate.Gate) *registry {
	reg := &registry{commands: make(map[string]*Command, len(allCommands)), gate: gate}
	for _, cmd := range allCommands {
		c := cmd
		if _, dup := reg.commands[c.Name]; dup {
			panic("console: duplicate command name " + c.Name)
		}
		reg.commands[c.Name] = &c
		reg.order = append(reg.order, c.Name)
	}
	return reg
}

func (r *registry) lookup(name string) (*Command, bool) {
	cmd, ok := r.commands[name]
	if !ok || !r.gate.Allows(cmd.Plugin) {
		return nil, false
	}
	return cmd, true
}

// CommandsOf lists the commands plugin registered, for its manifest.
func CommandsOf(plugin string) []string {
	var out []string
	for _, cmd := range allCommands {
		if cmd.Plugin == plugin {
			out = append(out, cmd.Name)
		}
	}
	return out
}

// visible is what help, Spec and Complete show: every command the actor is
// allowed to run, in registration order. A command the actor cannot run is
// not merely unlisted here — running it by name still answers "permission
// denied", which is Execute's job (it calls lookup directly, not visible).
func (r *registry) visible(actor user.User) []*Command {
	out := make([]*Command, 0, len(r.order))
	for _, name := range r.order {
		cmd := r.commands[name]
		if r.gate.Allows(cmd.Plugin) && hasPermission(actor, cmd.Permission) {
			out = append(out, cmd)
		}
	}
	return out
}

// Anyone is the Permission of a command every signed-in account may run —
// the ones that act on the caller's own account and nothing else.
//
// Spelled as a word rather than as "" because "" already means "any
// administrator", and the two are the opposite of each other. A command
// that forgets to set Permission gets the stricter of the two, which is the
// right way round for a mistake to fall.
const Anyone = "anyone"

// hasPermission mirrors internal/admin/permissions.go's hasPermission
// except for two console-specific selectors: Anyone means any signed-in
// account, and super_admin checks that exact role. Any one ordinary grant
// in the comma list is enough and "" means any administrator. It is copied rather than shared
// because the admin package does not export it — this copy is only ever the
// console's own early, friendly refusal; the dispatched request re-checks
// the real one from inside the mux, which is the actual control, and for an
// Anyone command that check is auth.RequireUser on the route it calls
// rather than auth.RequireAdmin (see dispatch.go).
func hasPermission(actor user.User, permissions string) bool {
	if permissions == Anyone {
		return true
	}
	if permissions == "super_admin" {
		return actor.IsSuperAdmin()
	}
	if permissions == "" {
		return actor.IsAdmin()
	}
	for _, permission := range strings.Split(permissions, ",") {
		if actor.CanAdmin(permission) {
			return true
		}
	}
	return false
}

// CallError is what Runtime.Call returns for an HTTP-level failure (status
// >= 400). It carries the admin API's own error code so the renderer can
// show it dim beside the message, exactly as section 1.5 of the contract
// asks: "error: <message>" plus the code, HTTP status only when there is no
// message.
type CallError struct {
	Status  int
	Code    string
	Message string
}

func (e *CallError) Error() string { return e.Message }

// Runtime is what a command's Run function receives — the whole surface a
// command author needs, and (deliberately) nothing else. See the package
// comment above for how it is used end to end.
type Runtime struct {
	Ctx     context.Context
	Session *Session
	Out     io.Writer

	console  *Console
	cmd      *Command
	args     []string
	flags    map[string]string
	yes      bool
	jsonFlag bool
	rawJSON  []byte
}

// Arg returns the i-th positional argument, or "" past the end — a command
// checks NArg first when an argument is required.
func (rt *Runtime) Arg(i int) string {
	if i < 0 || i >= len(rt.args) {
		return ""
	}
	return rt.args[i]
}

// Args returns every positional argument, in order. watch's Run is the one
// place this matters beyond convenience: it re-dispatches these exact
// tokens as a nested command line without re-tokenizing them, so a quoted
// argument survives intact.
func (rt *Runtime) Args() []string { return rt.args }

// NArg is the positional argument count.
func (rt *Runtime) NArg() int { return len(rt.args) }

// Present reports whether flag was given on the line at all, boolean or
// not. name may be spelled either way ("q" or "--q").
func (rt *Runtime) Present(name string) bool {
	_, ok := rt.flags[normalizeFlagName(name)]
	return ok
}

// String is the flag's raw value, or "" if it was not given or is a
// boolean flag. Use StringOr for a default other than "".
func (rt *Runtime) String(name string) string {
	return rt.flags[normalizeFlagName(name)]
}

// StringOr is String, falling back to def when the flag is absent.
func (rt *Runtime) StringOr(name, def string) string {
	if rt.Present(name) {
		return rt.String(name)
	}
	return def
}

// Int parses the flag's value as a base-10 integer; 0 if absent or not a
// number. Use IntOr for any other default.
func (rt *Runtime) Int(name string) int {
	n, err := strconv.Atoi(rt.flags[normalizeFlagName(name)])
	if err != nil {
		return 0
	}
	return n
}

// IntOr is Int, falling back to def when the flag is absent.
func (rt *Runtime) IntOr(name string, def int) int {
	if rt.Present(name) {
		return rt.Int(name)
	}
	return def
}

// Bool reports whether a boolean flag is set. A bare "--hidden" is true; an
// explicit "--hidden=false" is false; an absent flag is false.
func (rt *Runtime) Bool(name string) bool {
	raw, ok := rt.flags[normalizeFlagName(name)]
	if !ok {
		return false
	}
	if raw == "" {
		return true
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		// A malformed explicit value ("--hidden=maybe") still means the
		// flag was given, and a command asking a plain yes/no question
		// should not silently read that as "no".
		return true
	}
	return b
}

// Duration parses the flag's value with time.ParseDuration; 0 if absent or
// unparseable. Use DurationOr for any other default.
func (rt *Runtime) Duration(name string) time.Duration {
	d, err := time.ParseDuration(rt.flags[normalizeFlagName(name)])
	if err != nil {
		return 0
	}
	return d
}

// DurationOr is Duration, falling back to def when the flag is absent.
func (rt *Runtime) DurationOr(name string, def time.Duration) time.Duration {
	if rt.Present(name) {
		return rt.Duration(name)
	}
	return def
}

// Confirmed reports whether --yes (or -y) was given. Destructive commands
// do not need to call this to refuse — Execute already does, before Run is
// ever invoked — but a command may still want to branch on it.
func (rt *Runtime) Confirmed() bool { return rt.yes }

func normalizeFlagName(name string) string { return strings.TrimLeft(name, "-") }

// Call performs one admin API request as the session's actor and decodes
// the JSON response generically (into the same shapes encoding/json always
// produces: map[string]any, []any, string, float64, bool, or nil).
//
// A non-nil error is either a transport failure (should not happen for an
// in-process call) or, for a status >= 400, a *CallError carrying the admin
// API's own code and message — return it from Run as-is and the engine
// renders it exactly the way a failed dispatch is supposed to look.
func (rt *Runtime) Call(method, path string, body any) (any, int, error) {
	resp, err := rt.console.opts.Dispatch(rt.Ctx, rt.Session.Actor, method, path, body)
	if err != nil {
		return nil, 0, err
	}
	rt.rawJSON = resp.Body

	var decoded any
	if len(resp.Body) > 0 {
		if jsonErr := json.Unmarshal(resp.Body, &decoded); jsonErr != nil {
			return nil, resp.Status, fmt.Errorf("console: decode response: %w", jsonErr)
		}
	}
	if resp.Status >= 400 {
		failure := newCallError(resp.Status, decoded)
		if hint := twoFactorHint(failure.Code, rt.Session.Lang); hint != "" {
			failure.Message = hint
		}
		return decoded, resp.Status, failure
	}
	return decoded, resp.Status, nil
}

// twoFactorHint turns the backoffice's two refusals into the command that
// answers them. In the browser the page draws a place to type the code; here
// the code is typed by hand, so the refusal has to say where.
func twoFactorHint(code, lang string) string {
	switch code {
	case "two_factor_backoffice_verify":
		if lang == "zh" {
			return "此命令需要先输入验证码：2fa backoffice <验证码>（验证器应用中的六位数字，或一个恢复码）"
		}
		return "this command needs a code first: 2fa backoffice <code> (six digits from your app, or a recovery code)"
	case "two_factor_backoffice":
		if lang == "zh" {
			return "此命令需要先为你的账户开启两步验证：2fa setup"
		}
		return "this command needs two-step sign-in on your account first: 2fa setup"
	}
	return ""
}

// CallRaw performs one admin API request exactly like Call, but returns the
// response body untouched instead of decoding it as JSON. It exists for the
// one admin route that does not answer JSON at all —
// POST /api/admin/health/probe, a text/event-stream response ParseSSE turns
// into frames — where Call's own json.Unmarshal would always fail on a
// well-formed response and mask a real transport error behind a decode
// error. Every other command uses Call.
func (rt *Runtime) CallRaw(method, path string, body any) ([]byte, int, error) {
	resp, err := rt.console.opts.Dispatch(rt.Ctx, rt.Session.Actor, method, path, body)
	if err != nil {
		return nil, 0, err
	}
	rt.rawJSON = resp.Body
	if resp.Status >= 400 {
		var decoded any
		if len(resp.Body) > 0 {
			_ = json.Unmarshal(resp.Body, &decoded)
		}
		return resp.Body, resp.Status, newCallError(resp.Status, decoded)
	}
	return resp.Body, resp.Status, nil
}

func newCallError(status int, decoded any) *CallError {
	message := fmt.Sprintf("request failed with status %d", status)
	code := ""
	if envelope, ok := decoded.(map[string]any); ok {
		if inner, ok := envelope["error"].(map[string]any); ok {
			if s, ok := inner["message"].(string); ok && s != "" {
				message = s
			}
			if s, ok := inner["code"].(string); ok {
				code = s
			}
		}
	}
	return &CallError{Status: status, Code: code, Message: message}
}

func (rt *Runtime) effectiveJSON() bool { return rt.Session.JSON || rt.jsonFlag }

// Table renders rows as an aligned grid, unless JSON output is in effect
// (the session's `format json`, or this line's own --json), in which case
// it prints the server's own response body — the payload the most recent
// Call captured — pretty-printed and nothing else, so `ssh admin@host
// 'user list --json' | jq` sees exactly what the API answered.
//
// A command that builds a table without ever calling the API (there are
// none among the session commands, but a future local one could) still
// gets sensible --json output: the rows are encoded as an array of
// header-keyed objects instead.
func (rt *Runtime) Table(headers []string, rows [][]string) error {
	if rt.effectiveJSON() {
		if len(rt.rawJSON) > 0 {
			return RenderJSON(rt.Out, rt.rawJSON)
		}
		return renderRowsAsJSON(rt.Out, headers, rows)
	}
	RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, headers, rows)
	return nil
}

// Fields renders a key: value block — the "show" counterpart to Table, with
// the same JSON-passthrough behaviour.
func (rt *Runtime) Fields(pairs [][2]string) error {
	if rt.effectiveJSON() {
		if len(rt.rawJSON) > 0 {
			return RenderJSON(rt.Out, rt.rawJSON)
		}
		return renderPairsAsJSON(rt.Out, pairs)
	}
	RenderFields(rt.Out, rt.Session.Colour, pairs)
	return nil
}

// Printf writes plain text to the session, with no ANSI of its own — a
// command that wants colour uses Table/Fields/Errorf, which already know
// the session's rules.
func (rt *Runtime) Printf(format string, args ...any) {
	fmt.Fprintf(rt.Out, format, args...)
}

// Errorf builds an error the way fmt.Errorf does. It does not write
// anything itself — Execute renders whatever error Run returns exactly
// once, through the same path a *CallError takes, so a command never needs
// to know how errors are displayed.
func (rt *Runtime) Errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
