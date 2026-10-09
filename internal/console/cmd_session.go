package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The eleven session commands: help, clear, exit, quit, whoami, version,
// history, watch, lang, format, echo. None of them call the API, so they are
// open to every account that can open the terminal at all — help lists only
// what the caller may run, and watch repeats a command that is checked again
// each time — and none is Destructive, which is
// also exactly registry.go's unaudited list, echo and watch aside: echo is
// audited-exempt because it calls nothing, and watch is deliberately NOT
// exempt because every command it repeats is worth its own record.
func init() {
	registerCommand(Command{
		Name:       "help",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "List commands, or explain one", ZH: "列出命令，或说明某个命令"},
		Usage:      "help [<command>|<noun>|ssh|keys] [-k <word>]",
		Help: Text{
			EN: "With nothing after it, help lists every command you may run, grouped by area. " +
				"help <command> (or <command> --help) explains one command in full. help <noun> " +
				"lists that noun's verbs. help -k <word> searches names, summaries, flags and " +
				"examples in either language.",
			ZH: "不带参数时列出你可以使用的全部命令，按区域分组。help <命令>（或 <命令> --help）会完整说明某个命令。" +
				"help <名词> 列出该名词下的全部动词。help -k <关键字> 会在中英文的名称、说明、选项与示例中搜索。",
		},
		Flags: []Flag{
			{Name: "--search", Short: "-k", Hint: Text{EN: "search commands", ZH: "搜索命令"}, Value: "WORD"},
		},
		Examples: []string{"help", "help user edit", "help user", "help -k password", "help ssh", "help keys"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.Present("search") {
				renderSearch(rt.Out, rt.Session, rt.String("search"), rt.console.reg)
				return nil
			}
			if rt.NArg() == 0 {
				renderHelpIndex(rt.Out, rt.Session, rt.console.reg)
				return nil
			}

			args := rt.Args()
			if len(args) == 1 && args[0] == "ssh" {
				renderSSHHelp(rt.Out, rt.Session, rt.console)
				return nil
			}
			if len(args) == 1 && args[0] == "keys" {
				renderKeysHelp(rt.Out, rt.Session)
				return nil
			}

			joined := strings.Join(args, " ")
			if cmd, ok := rt.console.reg.lookup(joined); ok && hasPermission(rt.Session.Actor, cmd.Permission) {
				renderCommandHelp(rt.Out, rt.Session, cmd, rt.console.reg)
				return nil
			}
			if renderNounHelp(rt.Out, rt.Session, args[0], rt.console.reg) {
				return nil
			}
			if rt.Session.Lang == "zh" {
				return rt.Errorf("没有名为 %q 的命令或主题。", joined)
			}
			return rt.Errorf("no command or topic named %q", joined)
		},
	})

	registerCommand(Command{
		Name:       "clear",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Clear the screen", ZH: "清屏"},
		Usage:      "clear",
		Examples:   []string{"clear"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.Session.Colour {
				fmt.Fprint(rt.Out, "\x1b[H\x1b[2J")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:       "exit",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "End this session", ZH: "结束本次会话"},
		Usage:      "exit",
		Help: Text{
			EN: "Same as Ctrl-D on an empty line over SSH, or closing the tab on the web terminal.",
			ZH: "效果与在 SSH 中于空行按 Ctrl-D，或在网页终端关闭该标签页相同。",
		},
		Examples: []string{"exit"},
		SeeAlso:  []string{"quit"},
		Run:      func(_ context.Context, _ *Runtime) error { return errExit },
	})

	registerCommand(Command{
		Name:       "quit",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "End this session", ZH: "结束本次会话"},
		Usage:      "quit",
		Examples:   []string{"quit"},
		SeeAlso:    []string{"exit"},
		Run:        func(_ context.Context, _ *Runtime) error { return errExit },
	})

	registerCommand(Command{
		Name:       "whoami",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Show who you are signed in as", ZH: "显示当前登录身份"},
		Usage:      "whoami",
		Examples: []string{
			"whoami",
			"whoami --json",
		},
		Run: func(_ context.Context, rt *Runtime) error {
			actor := rt.Session.Actor
			lang := rt.Session.Lang

			perms := "—"
			switch {
			case actor.IsSuperAdmin():
				if lang == "zh" {
					perms = "全部（超级管理员）"
				} else {
					perms = "all (super admin)"
				}
			case len(actor.AdminPermissions) > 0:
				perms = strings.Join(actor.AdminPermissions, ", ")
			}

			return rt.Fields([][2]string{
				{"username", actor.Username},
				{"role", string(actor.Role)},
				{"permissions", perms},
				{"transport", rt.Session.Transport},
			})
		},
	})

	registerCommand(Command{
		Name:       "version",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Show the running build version", ZH: "显示当前运行的构建版本"},
		Usage:      "version",
		Examples: []string{
			"version",
		},
		Run: func(_ context.Context, rt *Runtime) error {
			v := rt.console.opts.Version
			if v == "" {
				v = "dev"
			}
			rt.Printf("%s\n", v)
			return nil
		},
	})

	registerCommand(Command{
		Name:       "history",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Where your command history lives", ZH: "历史记录保存在哪里"},
		Usage:      "history",
		Help: Text{
			EN: "History belongs to your own terminal, not to the server: the web terminal keeps it " +
				"per tab, and an SSH client keeps it per connection with Ctrl-R to search it.",
			ZH: "历史记录属于你自己的终端，而不是服务器：网页终端按标签页保存，SSH 客户端按连接保存，可用 Ctrl-R 搜索。",
		},
		Examples: []string{"history"},
		SeeAlso:  []string{"help keys"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.Session.Lang == "zh" {
				rt.Printf("历史记录保存在你的终端里：网页终端用 ↑/↓ 翻阅；SSH 同样支持 ↑/↓，并可用 Ctrl-R 搜索。\n")
				return nil
			}
			rt.Printf("History lives in your own terminal: ↑/↓ in the web terminal, or ↑/↓ and Ctrl-R to search over SSH.\n")
			return nil
		},
	})

	registerCommand(Command{
		Name:       "watch",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Repeat a command until stopped", ZH: "反复执行某个命令，直到停止"},
		Usage:      "watch [--interval DURATION] [--count N] [--for DURATION] <command...>",
		Help: Text{
			EN: "Re-runs the given command, clearing between runs, until the connection is cancelled " +
				"(Ctrl-C / closing the tab), --count is reached or --for runs out (default 1h, at most 24h). " +
				"A flag meant for the wrapped " +
				"command rather than for watch itself goes after a literal --, e.g. " +
				"watch --interval 5s -- user list --q alice.",
			ZH: "反复执行给定的命令，每次之间清屏，直到连接被取消（Ctrl-C / 关闭标签页）、达到 --count 次数或 --for 用完" +
				"（默认 1 小时，最长 24 小时）。" +
				"若某个选项是给被包裹的命令而非 watch 本身的，请放在字面量 -- 之后，例如" +
				" watch --interval 5s -- user list --q alice。",
		},
		Flags: []Flag{
			{Name: "--interval", Hint: Text{EN: "how often to re-run, minimum 1s", ZH: "重复间隔，最短 1 秒"}, Value: "DURATION", Default: "2s"},
			{Name: "--count", Hint: Text{EN: "stop after this many runs, 0 = until --for", ZH: "达到该次数后停止，0 表示直到 --for 用完"}, Value: "N", Default: "0"},
			{Name: "--for", Hint: Text{EN: "stop after this long, default 1h, at most 24h", ZH: "运行这么久后停止，默认 1 小时，最长 24 小时"}, Value: "DURATION", Default: "1h"},
		},
		Args: []Arg{
			{Name: "command...", Hint: Text{EN: "the command to repeat", ZH: "要重复执行的命令"}, Required: true},
		},
		Examples: []string{
			"watch usage rpm",
			"watch --interval 10s --count 6 health status",
		},
		Run: func(ctx context.Context, rt *Runtime) error {
			if rt.NArg() == 0 {
				return rt.Errorf("watch requires a command to repeat, e.g. watch usage rpm")
			}
			sub := rt.Args()
			if sub[0] == "watch" {
				return rt.Errorf("watch cannot watch itself")
			}

			interval := rt.DurationOr("interval", 2*time.Second)
			if interval < time.Second {
				interval = time.Second
			}
			count := rt.IntOr("count", 0)

			// A watch nobody stops would hold its session for as long as the
			// connection lived. The transport cancels ctx when the client goes
			// away; this bounds the client that stays and forgets.
			limit := rt.DurationOr("for", defaultWatchFor)
			if limit <= 0 || limit > maxWatchFor {
				limit = maxWatchFor
			}
			ctx, stop := context.WithTimeout(ctx, limit)
			defer stop()

			for i := 0; count == 0 || i < count; i++ {
				if rt.Session.Colour {
					fmt.Fprint(rt.Out, "\x1b[H\x1b[2J")
				} else if i > 0 {
					fmt.Fprintln(rt.Out, "────────")
				}

				result := rt.console.runTokens(ctx, rt.Session, rt.Out, sub, time.Now())
				if result.Exit {
					return errExit
				}
				if ctx.Err() != nil {
					watchStopped(rt, ctx, limit)
					return nil
				}
				if count != 0 && i == count-1 {
					break
				}

				select {
				case <-ctx.Done():
					watchStopped(rt, ctx, limit)
					return nil
				case <-time.After(interval):
				}
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:       "lang",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Switch the console's language for this session", ZH: "切换本次会话的控制台语言"},
		Usage:      "lang <en|zh>",
		Args: []Arg{
			{Name: "language", Hint: Text{EN: "en or zh", ZH: "en 或 zh"}, Required: true},
		},
		Examples: []string{"lang zh", "lang en"},
		Run: func(_ context.Context, rt *Runtime) error {
			switch rt.Arg(0) {
			case "en", "zh":
				rt.Session.Lang = rt.Arg(0)
				return nil
			default:
				return rt.Errorf("lang must be en or zh, not %q", rt.Arg(0))
			}
		},
	})

	registerCommand(Command{
		Name:       "format",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Switch between table and JSON output for this session", ZH: "切换本次会话的输出格式：表格或 JSON"},
		Usage:      "format <table|json>",
		Help: Text{
			EN: "Changes how list and show commands render for the rest of this session. A single " +
				"command's own --json flag does the same for one line without changing this default.",
			ZH: "更改本次会话中列表与详情类命令的展示方式。单条命令自带的 --json 选项效果相同，但不会改变这个默认设置。",
		},
		Args: []Arg{
			{Name: "mode", Hint: Text{EN: "table or json", ZH: "table 或 json"}, Required: true},
		},
		Examples: []string{"format json", "format table"},
		SeeAlso:  []string{"help keys"},
		Run: func(_ context.Context, rt *Runtime) error {
			switch rt.Arg(0) {
			case "json":
				rt.Session.JSON = true
				return nil
			case "table":
				rt.Session.JSON = false
				return nil
			default:
				return rt.Errorf("format must be table or json, not %q", rt.Arg(0))
			}
		},
	})

	registerCommand(Command{
		Name:       "echo",
		Group:      "session",
		Permission: Anyone,
		Summary:    Text{EN: "Print the given text back", ZH: "原样输出给定文本"},
		Usage:      "echo <text...>",
		Help: Text{
			EN: "Mostly useful for testing quoting and a script's own output, not for anything the " +
				"admin API does.",
			ZH: "主要用于测试引号处理和脚本自身的输出，与管理 API 无关。",
		},
		Examples: []string{`echo hello`, `echo "quoted text"`},
		Run: func(_ context.Context, rt *Runtime) error {
			rt.Printf("%s\n", strings.Join(rt.Args(), " "))
			return nil
		},
	})
}

// What `watch` runs for when --for is not given, and the longest it may be
// told to.
const (
	defaultWatchFor = time.Hour
	maxWatchFor     = 24 * time.Hour
)

// watchStopped says why a watch ended, but only when it was the clock: a
// cancelled connection has nobody left to tell.
func watchStopped(rt *Runtime, ctx context.Context, limit time.Duration) {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return
	}
	if rt.Session.Lang == "zh" {
		rt.Printf("\nwatch 已运行 %s，到时间停止。\n", limit)
		return
	}
	rt.Printf("\nwatch stopped after %s.\n", limit)
}
