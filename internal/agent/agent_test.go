package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// seen records what the console actually dispatched, which is the only thing
// worth asserting about an assembled command line: not how it was spelled,
// but which request it turned into.
type seen struct {
	method string
	path   string
	body   any
}

func broker(t *testing.T, log *[]seen) *Broker {
	t.Helper()
	engine := console.New(console.Options{
		Version: "test",
		Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (console.Response, error) {
			*log = append(*log, seen{method: method, path: path, body: body})
			return console.Response{Status: 200, Body: []byte(`{"ok":true}`)}, nil
		},
	})
	return New(engine)
}

func admin() user.User {
	return user.User{ID: "u-admin", Username: "root", Role: user.RoleSuperAdmin, Status: user.StatusActive}
}

func reader() user.User {
	return user.User{ID: "u-reader", Username: "reader", Role: user.RoleUser, Status: user.StatusActive}
}

// The whole permission story, in one assertion: the tool list is the account's
// own visible command set, so an ordinary reader is not offered the
// administrative half and cannot ask for it.
func TestTheToolListIsWhatTheAccountMayRun(t *testing.T) {
	var log []seen
	b := broker(t, &log)

	names := func(actor user.User) map[string]bool {
		out := map[string]bool{}
		for _, tool := range b.Offer(context.Background(), actor) {
			out[tool.Name] = true
		}
		return out
	}

	forAdmin, forReader := names(admin()), names(reader())

	if !forAdmin["user_list"] {
		t.Error("an administrator was not offered user_list")
	}
	if forReader["user_list"] {
		t.Error("an ordinary account was offered the administrative user_list")
	}
	if !forReader["me_show"] {
		t.Error("an ordinary account was not offered its own me_show")
	}
	if len(forReader) == 0 {
		t.Fatal("an ordinary account was offered nothing at all")
	}
	if len(forReader) >= len(forAdmin) {
		t.Errorf("reader sees %d tools and admin %d; the filter is not filtering",
			len(forReader), len(forAdmin))
	}

	// A session command changes a terminal this model does not have.
	if forAdmin["help"] || forAdmin["clear"] || forAdmin["lang"] {
		t.Error("a session command was offered as a tool")
	}
}

func TestEveryToolNameAndSchemaIsWellFormed(t *testing.T) {
	var log []seen
	for _, tool := range broker(t, &log).Offer(context.Background(), admin()) {
		if strings.ContainsAny(tool.Name, " |/") {
			t.Errorf("tool name %q carries a character the protocols reject", tool.Name)
		}
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			t.Errorf("%s: schema is not JSON: %v", tool.Name, err)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("%s: schema type = %q", tool.Name, schema.Type)
		}
		for _, name := range schema.Required {
			if _, ok := schema.Properties[name]; !ok {
				t.Errorf("%s: requires %q, which is not a property", tool.Name, name)
			}
		}
	}
}

// The engine owns -h, --help, --json, -y and --yes on every command, so a
// flag by one of those names could never be reached. A positional argument
// may legitimately be called json — three import commands take the document
// itself that way — and that is not the same thing.
func TestNoCommandDeclaresAFlagTheEngineOwns(t *testing.T) {
	var log []seen
	_ = broker(t, &log)
	for _, command := range console.New(console.Options{}).Spec(&console.Session{Actor: admin()}).Commands {
		for _, flag := range command.Flags {
			switch paramName(flag.Name) {
			case "help", "h", "json", "yes", "y":
				t.Errorf("%s declares %s, which the engine already owns", command.Name, flag.Name)
			}
		}
	}
}

// Positional arguments and flags share one properties object, so two of them
// reduced to the same key would silently take each other's value on the way
// back out to a command line.
func TestNoCommandHasTwoParametersWithTheSameName(t *testing.T) {
	for _, command := range console.New(console.Options{}).Spec(&console.Session{Actor: admin()}).Commands {
		seenNames := map[string]string{}
		note := func(kind, raw string) {
			key := paramName(raw)
			if previous, clash := seenNames[key]; clash {
				t.Errorf("%s: %s %q and %s both become %q", command.Name, kind, raw, previous, key)
			}
			seenNames[key] = raw
		}
		for _, arg := range command.Args {
			note("argument", arg.Name)
		}
		for _, flag := range command.Flags {
			note("flag", flag.Name)
		}
	}
}

func TestACallBecomesTheRequestItNames(t *testing.T) {
	var log []seen
	b := broker(t, &log)

	output, failed := b.Run(context.Background(), admin(), adapter.ToolCall{
		Name:      "user_list",
		Arguments: `{"q":"alice","limit":20}`,
	})
	if failed {
		t.Fatalf("the call was refused: %s", output)
	}
	if len(log) != 1 {
		t.Fatalf("%d requests dispatched, want 1", len(log))
	}
	if log[0].method != "GET" || !strings.HasPrefix(log[0].path, "/api/admin/users?") {
		t.Errorf("dispatched %s %s", log[0].method, log[0].path)
	}
	if !strings.Contains(log[0].path, "q=alice") || !strings.Contains(log[0].path, "limit=20") {
		t.Errorf("the arguments did not reach the request: %s", log[0].path)
	}
}

// A number arrives from JSON as a float. Sent on as "20.000000" it is a
// different request, and as "2e+01" it is not a request at all.
func TestAWholeNumberTravelsWithoutADecimalPoint(t *testing.T) {
	if got := literal(float64(20)); got != "20" {
		t.Errorf("literal(20) = %q", got)
	}
	if got := literal(float64(1.5)); got != "1.5" {
		t.Errorf("literal(1.5) = %q", got)
	}
}

// A value with a space in it is one argument, not two, and the tokenizer
// only understands two escapes — so those are the only two to write.
func TestQuotingRoundTripsThroughTheTokenizer(t *testing.T) {
	for _, value := range []string{
		"plain",
		"two words",
		`has "quotes" in it`,
		`back\slash`,
		"a\nnewline",
		`{"json":"body"}`,
		"",
	} {
		tokens, err := console.Tokenize("cmd " + quote(value))
		if err != nil {
			t.Errorf("%q: tokenize: %v", value, err)
			continue
		}
		if len(tokens) != 2 {
			t.Errorf("%q became %d tokens: %q", value, len(tokens), tokens)
			continue
		}
		if tokens[1] != value {
			t.Errorf("%q round-tripped as %q", value, tokens[1])
		}
	}
}

// The one thing an agent must not be able to do. --yes is absent from every
// schema, the broker never adds it, and the engine refuses without it — so a
// destructive command costs a round and changes nothing.
func TestADestructiveCommandIsRefusedRatherThanRun(t *testing.T) {
	var log []seen
	b := broker(t, &log)

	output, failed := b.Run(context.Background(), admin(), adapter.ToolCall{
		Name:      "user_delete",
		Arguments: `{"id_username":"alice"}`,
	})
	if !failed {
		t.Fatalf("a destructive command ran: %s", output)
	}
	if len(log) != 0 {
		t.Fatalf("it reached the API anyway: %+v", log)
	}
	// The refusal is the output, not an empty string: a model that cannot
	// see why it was stopped simply tries again.
	if strings.TrimSpace(output) == "" {
		t.Error("the refusal said nothing")
	}
}

func TestAnUnknownToolIsAnAnswerRatherThanACrash(t *testing.T) {
	var log []seen
	output, failed := broker(t, &log).Run(context.Background(), reader(),
		adapter.ToolCall{Name: "user_delete", Arguments: `{}`})
	if !failed {
		t.Error("a reader reached an administrative command")
	}
	if !strings.Contains(output, "no such tool") {
		t.Errorf("output = %q", output)
	}
	if len(log) != 0 {
		t.Errorf("it dispatched anyway: %+v", log)
	}
}

func TestOversizedOutputIsCutWithTheCutDeclared(t *testing.T) {
	engine := console.New(console.Options{
		Version: "test",
		Dispatch: func(context.Context, user.User, string, string, any) (console.Response, error) {
			return console.Response{Status: 200, Body: []byte(`{"pad":"` +
				strings.Repeat("x", MaxOutputChars*2) + `"}`)}, nil
		},
	})
	output, failed := New(engine).Run(context.Background(), admin(),
		adapter.ToolCall{Name: "user_list", Arguments: `{}`})
	if failed {
		t.Fatalf("refused: %s", output)
	}
	if len([]rune(output)) > MaxOutputChars {
		t.Errorf("output is %d runes, cap is %d", len([]rune(output)), MaxOutputChars)
	}
}

// A value is a value whatever is in it. The tokenizer splits on every kind of
// whitespace, so a name carrying a no-break space and "--yes" used to come
// apart into two words, and the second confirmed a destructive command.
func TestAValueCannotBecomeAFlag(t *testing.T) {
	command := console.SpecCommand{
		Name:  "user delete",
		Args:  []console.SpecArg{{Name: "id|username", Required: true}},
		Flags: []console.SpecFlag{{Name: "--role", Value: "role"}},
	}
	for _, smuggled := range []string{"alice --yes", "alice\r--yes", "alice　-y", "--yes", "-y"} {
		arguments, _ := json.Marshal(map[string]string{"id_username": smuggled, "role": "user --yes"})
		line, err := commandLine(command, string(arguments))
		if err != nil {
			t.Fatal(err)
		}
		tokens, err := console.Tokenize(line)
		if err != nil {
			t.Fatalf("%q: tokenize %q: %v", smuggled, line, err)
		}
		parsed, err := console.ParseFlags(tokens[2:], []console.Flag{{Name: "--role", Value: "role"}})
		if err != nil {
			t.Fatalf("%q: parse %q: %v", smuggled, line, err)
		}
		if parsed.Yes {
			t.Errorf("%q confirmed the command: %q", smuggled, line)
		}
		if len(parsed.Args) != 1 || parsed.Args[0] != smuggled {
			t.Errorf("%q arrived as %q", smuggled, parsed.Args)
		}
		if parsed.Flags["role"] != "user --yes" {
			t.Errorf("the flag's value arrived as %q", parsed.Flags["role"])
		}
	}
}

// A model reads what other people wrote, so an administrator's agent must
// not change the instance at all: obeying a line planted in a nickname could
// otherwise promote its author. Reads still run.
func TestABackofficeWriteIsRefusedRatherThanRun(t *testing.T) {
	var log []seen
	b := broker(t, &log)

	output, failed := b.Run(context.Background(), admin(), adapter.ToolCall{
		Name:      "user_edit",
		Arguments: `{"id_username":"mallory","role":"super_admin"}`,
	})
	if !failed || !strings.Contains(output, "changes the instance") {
		t.Fatalf("a backoffice write was not refused as one: %v %s", failed, output)
	}
	if len(log) != 0 {
		t.Fatalf("it reached the API: %+v", log)
	}

	// And the console holds the same line for this transport, for a command
	// that calls more than it declares.
	var out strings.Builder
	result := b.console.Execute(context.Background(), b.session(admin()), &out, "user edit mallory --role super_admin")
	if result.OK {
		t.Errorf("the console ran a write for an agent session: %s", out.String())
	}
	for _, request := range log {
		if request.method != "GET" {
			t.Errorf("an agent session dispatched %s %s", request.method, request.path)
		}
	}
}

// The terminal's own gate holds here too: a group whose terminal is off is
// offered nothing and can run nothing.
func TestTheTerminalGateHoldsForTheAgent(t *testing.T) {
	var log []seen
	b := broker(t, &log)
	b.Allowed = func(context.Context, user.User) error { return errTerminalOff }

	if tools := b.Offer(context.Background(), reader()); len(tools) != 0 {
		t.Errorf("a reader without the terminal was offered %d tools", len(tools))
	}
	if _, failed := b.Run(context.Background(), reader(), adapter.ToolCall{Name: "me_show", Arguments: `{}`}); !failed {
		t.Error("a reader without the terminal ran a command")
	}
	if len(log) != 0 {
		t.Errorf("it dispatched anyway: %+v", log)
	}
}

var errTerminalOff = errorString("your group cannot use the terminal")

type errorString string

func (e errorString) Error() string { return string(e) }
