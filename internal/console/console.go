// Package console is the administrative console's engine: the same command
// set served two ways, over SSE to the browser terminal (internal/console's
// own HTTP handlers, in handlers.go) and over SSH (internal/consolessh,
// which calls Execute directly for an interactive session, and once per
// line for `ssh admin@host 'user list --json'`).
//
// Every command is a client of the same /api/admin/* endpoints the
// browser's own admin UI calls: Execute dispatches an in-process HTTP
// request through the real admin mux, behind the real auth.RequireAdmin and
// the real per-route permission check (see dispatch.go). That is the whole
// permission story — a console command cannot exceed what the actor could
// already do in the UI, because it is what the UI does. The engine's own
// Permission check (registry.go's hasPermission) only produces a cleaner
// refusal than a bare 403 would; it is never the only gate.
package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Response is what one in-process admin API call answered.
type Response struct {
	Status int
	Body   []byte
}

// AuditRecord is one executed command, handed to Options.Audit. Line has
// already had every Flag.Sensitive and Arg.Sensitive value replaced with
// *** — the engine builds it, not the transport, so a masking bug cannot
// leak a secret to two different callers in two different ways.
type AuditRecord struct {
	Actor     user.User
	Line      string
	Transport string
	IP        string
	OK        bool
	Code      string
}

// SSHInfo describes the SSH listener, for `help ssh` and the banner. The
// zero value means SSH is off.
type SSHInfo struct {
	Enabled     bool
	Addr        string
	Fingerprint string
}

// Options configures a Console. Dispatch is the only required field.
type Options struct {
	Dispatch Dispatcher
	Version  string
	SiteName func() string
	// Audit records one executed command. Never nil in production wiring;
	// a nil Audit simply means nothing is recorded, which is what lets a
	// unit test build a Console without a security-events store.
	Audit func(ctx context.Context, rec AuditRecord)
	SSH   SSHInfo
	// Which plugins are on; nil lets every command through.
	Plugins plugingate.Gate
	// SecretSetting reports whether a settings key holds a credential that
	// the console does not know of itself: those of plugins installed while
	// the server runs, which only the server's settings service has. Nil
	// means the compiled-in answer alone.
	SecretSetting func(key string) bool
}

// Console is the command engine. It holds no per-session state — Session
// carries that — so one Console safely serves every tab and every SSH
// connection at once.
type Console struct {
	reg  *registry
	opts Options
}

// New builds a Console from every command registered by a cmd_*.go file's
// init function (see registry.go). It panics on a duplicate command name or
// a command with no Run — both are programming errors caught the moment
// anything calls New, never a condition a caller needs to recover from.
func New(opts Options) *Console {
	return &Console{reg: newRegistry(opts.Plugins), opts: opts}
}

// Session is one terminal tab or one SSH channel. It is not safe for
// concurrent use by two commands at once — the transports that own it run
// one command at a time per session — but its fields are meant to be
// mutated: `lang`, `format` and (via the transport) Width all change it in
// place so later lines on the same session see the new value.
//
// History is the transport's own business, not the engine's: the web
// terminal keeps it in localStorage per tab, and consolessh's line editor
// keeps it per SSH channel. Neither belongs here, because neither survives
// the session anyway.
type Session struct {
	Actor     user.User
	Transport string // "web" | "ssh"
	IP        string
	Width     int  // terminal columns, >= 20; 0 means "unknown, assume 100"
	Colour    bool // may emit ANSI SGR and cursor/erase sequences
	Lang      string
	JSON      bool // `format json` is in effect for this session
}

// Result is what Execute answers after one line, whatever the command's own
// verdict was — a failed command has already written its own "error: …" to
// out by the time Result comes back; Result is for the transport, not the
// user.
type Result struct {
	OK      bool
	Code    string
	Exit    bool // `exit`/`quit`: the transport should close the session
	Elapsed time.Duration
}

// errExit is how `exit` and `quit` tell Execute to end the session, without
// giving every other command a way to fake the same thing by accident — it
// is unexported, so only cmd_session.go can produce it.
var errExit = errors.New("console: end session")

// unaudited is exactly the exclusion list section 4 of the contract names:
// every pure session command that touches no admin API and changes nothing
// but the session it runs in. Everything else — including `exit`/`quit`,
// which end the session, and `watch`, which repeats a command that is
// itself audited on every iteration — is worth a record.
var unaudited = map[string]bool{
	"help": true, "clear": true, "history": true, "whoami": true,
	"version": true, "lang": true, "format": true, "echo": true,
}

// Execute runs one line end to end: tokenize, resolve the command,
// check its permission, parse its flags, refuse a destructive command
// without --yes, run it, and render whatever it returns. Every one of
// those steps writes what the user should see to out before returning —
// Result is what the transport needs, not what the user reads.
func (c *Console) Execute(ctx context.Context, s *Session, out io.Writer, line string) Result {
	start := time.Now()
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return Result{OK: true, Elapsed: time.Since(start)}
	}

	tokens, err := Tokenize(trimmed)
	if err != nil {
		RenderError(out, s.Colour, s.JSON, err)
		return Result{Code: "parse_error", Elapsed: time.Since(start)}
	}
	return c.runTokens(ctx, s, out, tokens, start)
}

func (c *Console) runTokens(ctx context.Context, s *Session, out io.Writer, tokens []string, start time.Time) Result {
	if len(tokens) == 0 {
		return Result{OK: true, Elapsed: time.Since(start)}
	}

	name, rest, found := c.match(tokens)
	if !found {
		renderUnknown(out, s, name)
		return Result{Code: "unknown_command", Elapsed: time.Since(start)}
	}
	cmd, _ := c.reg.lookup(name)

	if !hasPermission(s.Actor, cmd.Permission) {
		renderPermissionDenied(out, s, cmd)
		return Result{Code: "permission_denied", Elapsed: time.Since(start)}
	}

	parsed, err := ParseFlags(rest, cmd.Flags)
	if err != nil {
		RenderError(out, s.Colour, s.JSON, err)
		return Result{Code: "parse_error", Elapsed: time.Since(start)}
	}

	if parsed.Help {
		renderCommandHelp(out, s, cmd, c.reg)
		return Result{OK: true, Elapsed: time.Since(start)}
	}

	if cmd.Destructive && !parsed.Yes {
		renderConfirmRequired(out, s, cmd)
		return Result{Code: "confirmation_required", Elapsed: time.Since(start)}
	}

	rt := &Runtime{
		Ctx:      ctx,
		Session:  s,
		Out:      out,
		console:  c,
		cmd:      cmd,
		args:     parsed.Args,
		flags:    parsed.Flags,
		yes:      parsed.Yes,
		jsonFlag: parsed.JSON,
	}

	runErr := cmd.Run(ctx, rt)
	elapsed := time.Since(start)

	ok := runErr == nil
	code := ""
	switch {
	case errors.Is(runErr, errExit):
		ok = true
	case runErr != nil:
		code = RenderError(out, s.Colour, rt.effectiveJSON(), runErr)
	}

	c.recordAudit(ctx, s, cmd, parsed, ok, code)

	if errors.Is(runErr, errExit) {
		return Result{OK: true, Exit: true, Elapsed: elapsed}
	}
	return Result{OK: ok, Code: code, Elapsed: elapsed}
}

// match finds the longest registered command name that is a prefix of
// tokens, up to 4 words — every command today is 1 or 2 words ("clear",
// "user edit"), and the cap just keeps a wildly long unknown line from
// costing more than a handful of map lookups.
func (c *Console) match(tokens []string) (name string, rest []string, found bool) {
	max := len(tokens)
	if max > 4 {
		max = 4
	}
	for n := max; n >= 1; n-- {
		candidate := strings.Join(tokens[:n], " ")
		if _, ok := c.reg.lookup(candidate); ok {
			return candidate, tokens[n:], true
		}
	}
	return tokens[0], tokens[1:], false
}

func (c *Console) recordAudit(ctx context.Context, s *Session, cmd *Command, parsed ParsedArgs, ok bool, code string) {
	if c.opts.Audit == nil || unaudited[cmd.Name] {
		return
	}
	// Detached like every other post-request write in this project
	// (internal/security's own Record calls, chat/http.go's save): the
	// command already ran and the actor already did it, so a reader
	// closing the tab a moment later must not erase the record of that.
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	c.opts.Audit(recCtx, AuditRecord{
		Actor:     s.Actor,
		Line:      c.auditLine(cmd, parsed),
		Transport: s.Transport,
		IP:        s.IP,
		OK:        ok,
		Code:      code,
	})
}

// auditLine rebuilds a readable command line from the parsed result rather
// than reusing the raw input, which is what lets it mask a Sensitive flag's
// value — the raw tokens have already forgotten which one that was.
//
// Positional arguments are masked the same way (Arg.Sensitive, and
// Command.SecretArgs for the ones that are secret only on some lines): the
// line is read by every administrator who holds the security grant, who are
// not the people a code or a credential was typed for.
func (c *Console) auditLine(cmd *Command, parsed ParsedArgs) string {
	var b strings.Builder
	b.WriteString(cmd.Name)
	var secret map[int]bool
	if cmd.SecretArgs != nil {
		secret = cmd.SecretArgs(c, parsed.Args)
	}
	for i, a := range parsed.Args {
		b.WriteByte(' ')
		if secret[i] || argSensitive(cmd, i) {
			b.WriteString("***")
			continue
		}
		b.WriteString(quoteIfNeeded(a))
	}
	for _, f := range cmd.Flags {
		key := normalizeFlagName(f.Name)
		value, present := parsed.Flags[key]
		if !present {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(f.Name)
		if f.Value != "" {
			if f.Sensitive {
				b.WriteString("=***")
			} else {
				b.WriteByte('=')
				b.WriteString(quoteIfNeeded(value))
			}
		}
	}
	if parsed.Yes {
		b.WriteString(" --yes")
	}
	if parsed.JSON {
		b.WriteString(" --json")
	}
	return b.String()
}

// secretSetting reports whether key is a settings key whose value must not be
// written to the audit trail. Three sources, because no one of them is the
// whole answer: the credentials the core defines, those a compiled-in plugin
// declared (settings.Lookup), and those of plugins installed at run time,
// which only the server's own settings service knows (Options.SecretSetting).
// The last line is a name check for the day a list is behind the keys: a
// credential that is logged once is logged for good, a harmless value masked
// by mistake costs a reader one `setting get`.
func (c *Console) secretSetting(key string) bool {
	switch key {
	case settings.TurnstileSecretKey, settings.OAuthGitHubSecret, settings.OAuthGoogleSecret, settings.OAuthOIDCClientSecret:
		return true
	}
	if d, ok := settings.Lookup(key); ok && d.Secret {
		return true
	}
	if c.opts.SecretSetting != nil && c.opts.SecretSetting(key) {
		return true
	}
	lower := strings.ToLower(key)
	for _, word := range []string{"secret", "password", "passwd", "token", "api_key", "apikey", "private_key", "credential"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// argSensitive reports whether the i-th positional is declared Sensitive,
// the last declaration standing for every token past it.
func argSensitive(cmd *Command, i int) bool {
	if len(cmd.Args) == 0 {
		return false
	}
	if i >= len(cmd.Args) {
		i = len(cmd.Args) - 1
	}
	return cmd.Args[i].Sensitive
}

// quoteIfNeeded quotes anything that could be mistaken for more than one
// token or for something other than text. The audit line is one row that a
// reader parses by eye: a value carrying a newline, an escape or a look-alike
// space could otherwise end that row early and start a forged one, or move
// the cursor of whoever reads it on a terminal.
func quoteIfNeeded(s string) string {
	if s == "" || strings.IndexFunc(s, func(r rune) bool {
		return r == '"' || r == '\'' || unicode.IsSpace(r) || !unicode.IsPrint(r)
	}) >= 0 {
		return strconv.Quote(s)
	}
	return s
}

// Banner is the SSH login banner and the web terminal's first block.
func (c *Console) Banner(s *Session) string {
	site := "Obsidian Arc"
	if c.opts.SiteName != nil {
		if name := sanitize(c.opts.SiteName()); name != "" {
			site = name
		}
	}
	// The nickname is free text the account chose; the banner is drawn on a
	// terminal like everything else.
	who := sanitize(s.Actor.DisplayName())

	var b strings.Builder
	if s.Lang == "zh" {
		fmt.Fprintf(&b, "%s — 终端\n", site)
		fmt.Fprintf(&b, "已登录：%s（%s）\n", who, roleLabel(s.Actor.Role, s.Lang))
		if c.opts.SSH.Enabled {
			fmt.Fprintf(&b, "SSH：%s，主机指纹 %s\n", c.opts.SSH.Addr, c.opts.SSH.Fingerprint)
		}
		b.WriteString("输入 'help' 开始，或 'help -k <关键字>' 搜索命令。")
	} else {
		fmt.Fprintf(&b, "%s — Terminal\n", site)
		fmt.Fprintf(&b, "Signed in as %s (%s)\n", who, roleLabel(s.Actor.Role, s.Lang))
		if c.opts.SSH.Enabled {
			fmt.Fprintf(&b, "SSH: %s, host fingerprint %s\n", c.opts.SSH.Addr, c.opts.SSH.Fingerprint)
		}
		b.WriteString("Type 'help' to get started, or 'help -k <word>' to search.")
	}
	return b.String()
}

func roleLabel(role user.Role, lang string) string {
	// Every account has a terminal now, so the banner names what the reader
	// is rather than assuming an administrator.
	if role == user.RoleUser {
		if lang == "zh" {
			return "成员"
		}
		return "member"
	}
	if role == user.RoleSuperAdmin {
		if lang == "zh" {
			return "超级管理员"
		}
		return "super admin"
	}
	if lang == "zh" {
		return "管理员"
	}
	return "admin"
}

func renderUnknown(out io.Writer, s *Session, name string) {
	if s.Lang == "zh" {
		fmt.Fprintf(out, "未知命令 %q。输入 'help' 查看可用命令。\n", name)
		return
	}
	fmt.Fprintf(out, "Unknown command %q. Type 'help' to see what is available.\n", name)
}

func renderPermissionDenied(out io.Writer, s *Session, cmd *Command) {
	grant := cmd.Permission
	if grant == "" {
		// Unreachable in practice: hasPermission("") is true for any
		// administrator, and only administrators ever reach Execute. Kept
		// so this function never prints an empty grant name if that
		// invariant is ever broken.
		grant = "administrators"
	}
	if s.Lang == "zh" {
		fmt.Fprintf(out, "权限不足：此命令需要 %q 权限。\n", grant)
		return
	}
	fmt.Fprintf(out, "permission denied: this command needs the %q grant\n", grant)
}

func renderConfirmRequired(out io.Writer, s *Session, cmd *Command) {
	if s.Lang == "zh" {
		fmt.Fprintf(out, "此操作不可撤销：加上 --yes（或 -y）确认，例如 %s --yes\n", cmd.Name)
		return
	}
	fmt.Fprintf(out, "this is destructive: add --yes (or -y) to confirm, e.g. %s --yes\n", cmd.Name)
}
