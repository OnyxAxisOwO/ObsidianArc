// Package agent is the work surface's tool broker: it offers the model the
// console commands the reader may run, and runs the ones it asks for.
//
// It adds no permission of its own, and that is the point. The tool list is
// console's own visible(actor) — the same list `help` prints for that
// account — and running one goes through console.Execute, which dispatches
// an in-process request through the real mux behind the real auth check. An
// agent cannot reach anything its reader could not reach by typing the same
// command, because it is typing the same command.
//
// Three things it deliberately does not do. It never runs a command that
// changes the instance through the backoffice — a destructive one, or any
// that writes to an /api/admin route — so the model has to tell the reader
// to run it themselves; the commands stay in the list so it can say which
// one. The model reads things other people wrote (feedback, names, logs),
// and an administrator's agent obeying a line planted there must not be able
// to promote its author. It never lets an argument become a flag: every
// value is passed after "--" or inside its own flag, so no "--yes" or
// "--role" can ride in on a name. And it never hides a refusal: the
// console's own error text goes back to the model as the tool's output,
// because a loop that cannot see why it was stopped will simply try again.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// MaxOutputChars is how much of a command's output the model is shown.
//
// Every round's output is re-sent as input on every round after it, so an
// unbounded `log list` would cost more on the fifth round than the answer
// is worth. The cut says it happened, so the model can narrow the command
// rather than assume it has read everything.
const MaxOutputChars = 8000

// Broker implements chat.ToolBroker over the console.
type Broker struct {
	console *console.Console
	// Allowed is the terminal's own gate — a group whose terminal is off —
	// asked again here, because the console is the same console whether it
	// is typed into or asked for by a model. Nil lets everyone through.
	Allowed func(ctx context.Context, actor user.User) error
}

func New(c *console.Console) *Broker { return &Broker{console: c} }

// Offer is the tool list for one account: every command it may run, named
// the way a tool has to be named.
func (b *Broker) Offer(ctx context.Context, actor user.User) []adapter.Tool {
	if b.Allowed != nil && b.Allowed(ctx, actor) != nil {
		return nil
	}
	spec := b.console.Spec(b.session(actor))
	out := make([]adapter.Tool, 0, len(spec.Commands))
	for _, command := range spec.Commands {
		if command.Name == "" || len(command.Endpoints) == 0 {
			// A session command — help, clear, lang — changes a terminal
			// this model does not have. Offering it is offering a no-op.
			continue
		}
		out = append(out, adapter.Tool{
			Name:        toolName(command.Name),
			Description: describe(command),
			Parameters:  schemaFor(command),
		})
	}
	return out
}

// Run executes one call and returns what to show the model.
func (b *Broker) Run(ctx context.Context, actor user.User, call adapter.ToolCall) (string, bool) {
	if b.Allowed != nil {
		if err := b.Allowed(ctx, actor); err != nil {
			return err.Error(), true
		}
	}
	spec := b.console.Spec(b.session(actor))
	var command *console.SpecCommand
	for i := range spec.Commands {
		if toolName(spec.Commands[i].Name) == call.Name {
			command = &spec.Commands[i]
			break
		}
	}
	if command == nil {
		// Not in this account's list, which is also the answer for a
		// command that does not exist: the model is told what it may use
		// rather than which of the two it got wrong.
		return "no such tool: " + call.Name, true
	}
	if changesInstance(*command) {
		return "this command changes the instance and cannot be run from here: " +
			"tell the reader the exact line to type in the console", true
	}

	line, err := commandLine(*command, call.Arguments)
	if err != nil {
		return err.Error(), true
	}

	var out bytes.Buffer
	result := b.console.Execute(ctx, b.session(actor), &out, line)

	answer := strings.TrimSpace(out.String())
	if answer == "" {
		if result.OK {
			answer = "(no output)"
		} else {
			answer = "the command failed and said nothing"
		}
	}
	return text.Truncate(answer, MaxOutputChars), !result.OK
}

// session is the console session an agent turn runs in.
//
// JSON because the model reads a response better than it reads a column
// layout, no colour because ANSI is noise in a prompt, and a transport of
// its own so the security log can tell a command an agent ran from one a
// person typed.
func (b *Broker) session(actor user.User) *console.Session {
	return &console.Session{
		Actor:     actor,
		Transport: "agent",
		Colour:    false,
		JSON:      true,
		Lang:      "en",
		Width:     100,
	}
}

// toolName is the command's name in the shape both protocols accept.
// "user list" cannot travel as a tool name; "user_list" can, and the
// mapping back is the same substitution.
func toolName(command string) string {
	return strings.ReplaceAll(command, " ", "_")
}

func describe(command console.SpecCommand) string {
	var b strings.Builder
	b.WriteString(command.Summary)
	if command.Usage != "" {
		b.WriteString("\nUsage: " + command.Usage)
	}
	if changesInstance(command) {
		// Said in the description rather than discovered by calling it: a
		// round spent on a refusal is a round the reader paid for.
		b.WriteString("\nChanges the instance: this cannot be run from here. " +
			"Tell the reader to run it themselves in the console.")
	}
	return b.String()
}

// changesInstance is whether a command destroys or writes anything through
// the backoffice. The account's own self-service commands stay runnable:
// they act on the reader alone, which the reader asked for. The console
// refuses the same writes for this transport on its side too, so a command
// that calls more than it declares is still held.
func changesInstance(command console.SpecCommand) bool {
	if command.Destructive {
		return true
	}
	for _, endpoint := range command.Endpoints {
		method, path, _ := strings.Cut(endpoint, " ")
		if method != "GET" && strings.HasPrefix(path, "/api/admin/") {
			return true
		}
	}
	return false
}

// schemaFor turns a command's positional arguments and flags into the one
// object a tool call carries.
//
// --yes is deliberately absent even on a destructive command: a schema that
// offers it is an invitation, and the engine's refusal is the only thing
// standing between a model and an irreversible command.
func schemaFor(command console.SpecCommand) json.RawMessage {
	properties := map[string]any{}
	var required []string

	for _, arg := range command.Args {
		name := paramName(arg.Name)
		properties[name] = map[string]any{
			"type": "string", "description": arg.Hint,
		}
		if arg.Required {
			required = append(required, name)
		}
	}
	for _, flag := range command.Flags {
		name := paramName(flag.Name)
		kind := "string"
		if flag.Value == "" {
			// No placeholder in the spec means the flag's presence is the
			// whole signal.
			kind = "boolean"
		}
		properties[name] = map[string]any{
			"type": kind, "description": flag.Hint,
		}
	}

	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return encoded
}

// paramName is a flag or argument name as a JSON property. Leading dashes
// go, and every character a property name should not carry becomes an
// underscore — "id|username" is a perfectly good hint and a poor key.
func paramName(name string) string {
	cleaned := strings.TrimLeft(name, "-")
	var b strings.Builder
	for _, r := range cleaned {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// commandLine assembles the line the console will run: flags first, each as
// one --name=value word, then "--", then the positional arguments.
//
// In that order so no value can be read as anything but a value. Positional
// arguments used to come first and travel bare, and the tokenizer splits on
// every kind of whitespace — a name carrying a no-break space and "--yes"
// became two words, and the second confirmed a destructive command the
// model was never offered the flag for. After "--" the console reads every
// word as positional, whatever it looks like.
func commandLine(command console.SpecCommand, arguments string) (string, error) {
	values := map[string]any{}
	if trimmed := strings.TrimSpace(arguments); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
			return "", fmt.Errorf("could not read the arguments as a JSON object: %v", err)
		}
	}

	parts := []string{command.Name}
	for _, flag := range command.Flags {
		value, ok := values[paramName(flag.Name)]
		if !ok {
			continue
		}
		if flag.Value == "" {
			// A boolean travels as the flag alone, and false is the absence
			// of it rather than "--hidden false".
			if truthy(value) {
				parts = append(parts, flag.Name)
			}
			continue
		}
		if strings.HasPrefix(flag.Name, "--") {
			parts = append(parts, flag.Name+"="+quote(literal(value)))
		} else {
			// A short-only flag has no = form; the parser takes the next word
			// as its value whatever it looks like, so it cannot be misread.
			parts = append(parts, flag.Name, quote(literal(value)))
		}
	}
	parts = append(parts, "--")
	for _, arg := range command.Args {
		value, ok := values[paramName(arg.Name)]
		if !ok {
			if arg.Required {
				return "", fmt.Errorf("%s needs %s", command.Name, arg.Name)
			}
			continue
		}
		parts = append(parts, quote(literal(value)))
	}
	return strings.Join(parts, " "), nil
}

func literal(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		// Every JSON number decodes as a float; the ones these commands take
		// are counts and ids, so a trailing .0 would be wrong on the wire.
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%g", typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "1" || typed == "yes"
	case float64:
		return typed != 0
	}
	return false
}

// quote wraps a value the way console.Tokenize reads it back: double
// quotes, with only the backslash and the quote itself escaped, because
// those are the only two escapes the tokenizer understands. Everything else
// — newlines included — survives literally, which is what a JSON body
// passed to `setting set` needs.
func quote(value string) string {
	if value == "" {
		return `""`
	}
	// Every rune the tokenizer splits on, not a list of the usual ones: it
	// splits on unicode.IsSpace, and a value left bare because it held a
	// no-break space instead of a space came apart into two words.
	if !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\'' || r == '\\'
	}) {
		return value
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
