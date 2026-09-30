package arc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Console is one run of a console command. What it prints is collected and
// drawn by the server's console — text, a table, or JSON when the operator
// asked for it — once the handler returns.
type Console struct {
	Command string
	Args    []string
	Flags   map[string]string
	// The operator asked for JSON output (--json).
	JSON bool
	// The operator confirmed a destructive command (--yes).
	Yes  bool
	Lang string

	ctx *Ctx
	out []map[string]any
}

// Arg is positional argument i, or "".
func (c *Console) Arg(i int) string {
	if i < 0 || i >= len(c.Args) {
		return ""
	}
	return c.Args[i]
}

// String is a flag's value, or "".
func (c *Console) String(flag string) string { return c.Flags[flag] }

// Present reports whether a flag was given.
func (c *Console) Present(flag string) bool { _, ok := c.Flags[flag]; return ok }

// IntOr is a flag as a number, or def when it is absent or not one.
func (c *Console) IntOr(flag string, def int) int {
	if n, err := strconv.Atoi(c.Flags[flag]); err == nil {
		return n
	}
	return def
}

// Printf prints a line of text.
func (c *Console) Printf(format string, args ...any) {
	c.out = append(c.out, map[string]any{"kind": "text", "text": fmt.Sprintf(format, args...)})
}

// Table prints a table; with --json the server prints it as objects instead.
func (c *Console) Table(headers []string, rows [][]string) {
	c.out = append(c.out, map[string]any{"kind": "table", "headers": headers, "rows": rows})
}

// Call runs an administrative endpoint as the operator, through the same
// checks a browser request passes. It needs the "console" permission.
func (c *Console) Call(method, path string, body any) (map[string]any, error) {
	raw, err := hostCall("console.call", map[string]any{"method": method, "path": path, "body": body})
	if err != nil {
		return nil, err
	}
	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := decodeNumbers(raw, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ResolveUser turns an account id or username into an id. It needs the
// "console" permission.
func (c *Console) ResolveUser(ref string) (string, error) {
	raw, err := hostCall("console.resolve_user", map[string]any{"ref": ref})
	if err != nil {
		return "", err
	}
	var id string
	err = json.Unmarshal(raw, &id)
	return id, err
}

func serveConsole(c *Ctx, raw json.RawMessage) (any, error) {
	var arg struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Flags   map[string]string `json:"flags"`
		JSON    bool              `json:"json"`
		Yes     bool              `json:"yes"`
	}
	if err := json.Unmarshal(raw, &arg); err != nil {
		return nil, err
	}
	h, ok := commands[arg.Command]
	if !ok {
		return nil, &Error{Status: 500, Code: "no_handler", Message: "no handler registered for " + arg.Command, Internal: true}
	}
	run := &Console{Command: arg.Command, Args: arg.Args, Flags: arg.Flags, JSON: arg.JSON, Yes: arg.Yes, Lang: c.Lang, ctx: c}
	if err := h(c, run); err != nil {
		// A command that lets a refused Call escape means the endpoint said no
		// to what the operator asked. Its sentence is the answer; passed on as
		// an ordinary error it would be printed as the plugin having crashed.
		var host *HostError
		if errors.As(err, &host) && host.Code == "console" {
			return nil, &Error{Status: 400, Code: host.Code, Message: host.Message}
		}
		return nil, err
	}
	return map[string]any{"out": run.out}, nil
}
