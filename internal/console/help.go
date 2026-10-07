package console

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// groupTitles maps a Command.Group slug to its bilingual heading in the
// `help` index. A group added later that is not listed here still renders
// — groupHeading falls back to the slug itself — but the fallback is
// English-only, so a new noun family's cmd_*.go should add its heading
// here rather than lean on it.
var groupTitles = map[string]Text{
	"session":     {EN: "Session", ZH: "会话"},
	"accounts":    {EN: "Accounts", ZH: "账户"},
	"groups":      {EN: "Groups", ZH: "分组"},
	"invites":     {EN: "Invites", ZH: "邀请码"},
	"catalogue":   {EN: "Providers & Models", ZH: "服务商与模型"},
	"operations":  {EN: "Operations", ZH: "运维"},
	"instance":    {EN: "Instance", ZH: "实例设置"},
	"feedback":    {EN: "Feedback", ZH: "用户反馈"},
	"chat":        {EN: "Chat", ZH: "对话"},
	"projects":    {EN: "Projects", ZH: "项目"},
	"credit":      {EN: "Credit & Quota", ZH: "额度与用量"},
	"bonus":       {EN: "Bonus", ZH: "赠金"},
	"keys":        {EN: "API Keys", ZH: "API 密钥"},
	"preferences": {EN: "Preferences", ZH: "偏好设置"},
	"profile":     {EN: "Profile", ZH: "个人资料"},
	"backup":      {EN: "Backup & Restore", ZH: "备份与恢复"},
	"images":      {EN: "Images", ZH: "生图"},
	"sandbox":     {EN: "Code Sandbox", ZH: "代码沙箱"},
}

func groupHeading(group, lang string) string {
	if t, ok := groupTitles[group]; ok {
		return t.For(lang)
	}
	return group
}

var (
	argumentsLabel = Text{EN: "Arguments:", ZH: "参数："}
	flagsLabel     = Text{EN: "Flags:", ZH: "选项："}
	examplesLabel  = Text{EN: "Examples:", ZH: "示例："}
	seeAlsoLabel   = Text{EN: "See also:", ZH: "另请参阅："}
	grantLabel     = Text{EN: "Requires:", ZH: "所需权限："}
	callsLabel     = Text{EN: "Calls:", ZH: "调用接口："}
	requiredMark   = Text{EN: " (required)", ZH: "（必填）"}
	defaultWord    = Text{EN: "default", ZH: "默认"}
	anyAdminWord   = Text{EN: "any administrator", ZH: "任意管理员"}

	helpFlagHint = Text{EN: "show help for this command", ZH: "显示此命令的帮助"}
	jsonFlagHint = Text{EN: "print the raw server response as JSON", ZH: "以 JSON 格式输出原始响应"}
	yesFlagHint  = Text{EN: "confirm a destructive command", ZH: "确认执行这项破坏性操作"}
)

// renderHelpIndex is bare `help`: every command the actor may run, grouped
// by noun, then a footer naming the ways to dig deeper.
func renderHelpIndex(out io.Writer, s *Session, reg *registry) {
	lang := s.Lang
	byGroup := map[string][]*Command{}
	var groupOrder []string
	for _, cmd := range reg.visible(s.Actor) {
		if _, ok := byGroup[cmd.Group]; !ok {
			groupOrder = append(groupOrder, cmd.Group)
		}
		byGroup[cmd.Group] = append(byGroup[cmd.Group], cmd)
	}
	sort.Strings(groupOrder)

	for _, group := range groupOrder {
		fmt.Fprintln(out, groupHeading(group, lang))
		cmds := byGroup[group]
		sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
		width := 0
		for _, cmd := range cmds {
			if l := len(cmd.Name); l > width {
				width = l
			}
		}
		for _, cmd := range cmds {
			fmt.Fprintf(out, "  %-*s  %s\n", width, cmd.Name, cmd.Summary.For(lang))
		}
		fmt.Fprintln(out)
	}

	if lang == "zh" {
		fmt.Fprintln(out, "输入 'help <命令>' 查看详情，'help -k <关键字>' 搜索命令，或 'help keys' 查看快捷键。")
	} else {
		fmt.Fprintln(out, "Type 'help <command>' for details, 'help -k <word>' to search, or 'help keys' for keyboard shortcuts.")
	}
}

// renderCommandHelp is `help <command>` and, identically, `<command>
// --help`: summary, usage, the extended description, arguments, flags with
// their defaults, at least two worked examples, see-also, and the grant it
// needs.
func renderCommandHelp(out io.Writer, s *Session, cmd *Command, reg *registry) {
	lang := s.Lang

	fmt.Fprintln(out, cmd.Summary.For(lang))
	fmt.Fprintln(out)
	if lang == "zh" {
		fmt.Fprintf(out, "用法： %s\n", cmd.Usage)
	} else {
		fmt.Fprintf(out, "Usage: %s\n", cmd.Usage)
	}

	if extra := cmd.Help.For(lang); extra != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, extra)
	}

	if len(cmd.Args) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, argumentsLabel.For(lang))
		width := 0
		for _, a := range cmd.Args {
			if l := len(a.Name); l > width {
				width = l
			}
		}
		for _, a := range cmd.Args {
			mark := ""
			if a.Required {
				mark = requiredMark.For(lang)
			}
			fmt.Fprintf(out, "  %-*s  %s%s\n", width, a.Name, a.Hint.For(lang), mark)
		}
	}

	if len(cmd.Flags) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, flagsLabel.For(lang))
		for _, f := range cmd.Flags {
			name := f.Name
			if f.Short != "" {
				name = f.Short + ", " + f.Name
			}
			if f.Value != "" {
				name += " " + f.Value
			}
			line := fmt.Sprintf("  %-28s %s", name, f.Hint.For(lang))
			if f.Default != "" {
				line += fmt.Sprintf(" (%s: %s)", defaultWord.For(lang), f.Default)
			}
			fmt.Fprintln(out, line)
		}
	}

	if len(cmd.Examples) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, examplesLabel.For(lang))
		for _, ex := range cmd.Examples {
			fmt.Fprintf(out, "  $ %s\n", ex)
		}
	}

	if len(cmd.SeeAlso) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "%s %s\n", seeAlsoLabel.For(lang), strings.Join(cmd.SeeAlso, ", "))
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s %s\n", grantLabel.For(lang), permissionDisplay(cmd.Permission, lang))
	if len(cmd.Endpoints) > 0 {
		fmt.Fprintf(out, "%s %s\n", callsLabel.For(lang), strings.Join(cmd.Endpoints, ", "))
	}

	_ = reg // reserved for a future "did you mean" pass over the registry
}

func permissionDisplay(permission, lang string) string {
	if permission == "" {
		return anyAdminWord.For(lang)
	}
	return permission
}

// renderNounHelp is `help <noun>`: every verb under that noun. It reports
// whether it found anything so the caller can fall through to "no such
// command or topic" when it did not.
func renderNounHelp(out io.Writer, s *Session, noun string, reg *registry) bool {
	var matches []*Command
	for _, cmd := range reg.visible(s.Actor) {
		if cmd.Name == noun || strings.HasPrefix(cmd.Name, noun+" ") {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		return false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })

	lang := s.Lang
	if lang == "zh" {
		fmt.Fprintf(out, "%s 的可用命令：\n", noun)
	} else {
		fmt.Fprintf(out, "Commands under %q:\n", noun)
	}
	width := 0
	for _, cmd := range matches {
		if l := len(cmd.Name); l > width {
			width = l
		}
	}
	for _, cmd := range matches {
		fmt.Fprintf(out, "  %-*s  %s\n", width, cmd.Name, cmd.Summary.For(lang))
	}
	return true
}

// renderSearch is `help -k <word>` / `help --search <word>`: every visible
// command whose name, summary, extended help, a flag's name or hint, or an
// example contains query, checked in both languages regardless of the
// session's own.
func renderSearch(out io.Writer, s *Session, query string, reg *registry) {
	lang := s.Lang
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if lang == "zh" {
			fmt.Fprintln(out, "用法： help -k <关键字>")
		} else {
			fmt.Fprintln(out, "Usage: help -k <word>")
		}
		return
	}

	var matches []*Command
	for _, cmd := range reg.visible(s.Actor) {
		if commandMatchesQuery(cmd, q) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		if lang == "zh" {
			fmt.Fprintf(out, "没有匹配 %q 的命令。\n", query)
		} else {
			fmt.Fprintf(out, "No commands match %q.\n", query)
		}
		return
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	width := 0
	for _, cmd := range matches {
		if l := len(cmd.Name); l > width {
			width = l
		}
	}
	for _, cmd := range matches {
		fmt.Fprintf(out, "  %-*s  %s\n", width, cmd.Name, cmd.Summary.For(lang))
	}
}

func commandMatchesQuery(cmd *Command, q string) bool {
	contains := func(s string) bool { return s != "" && strings.Contains(strings.ToLower(s), q) }
	if contains(cmd.Name) || contains(cmd.Summary.EN) || contains(cmd.Summary.ZH) ||
		contains(cmd.Help.EN) || contains(cmd.Help.ZH) {
		return true
	}
	for _, f := range cmd.Flags {
		if contains(f.Name) || contains(f.Hint.EN) || contains(f.Hint.ZH) {
			return true
		}
	}
	for _, ex := range cmd.Examples {
		if contains(ex) {
			return true
		}
	}
	return false
}

// renderSSHHelp is `help ssh`: how to connect, the host key fingerprint,
// one-shot usage, and the reminder that this is the console, never a
// server shell.
func renderSSHHelp(out io.Writer, s *Session, c *Console) {
	lang := s.Lang
	if !c.opts.SSH.Enabled {
		if lang == "zh" {
			fmt.Fprintln(out, "此实例未开启控制台的 SSH 访问。")
		} else {
			fmt.Fprintln(out, "SSH access to this console is not enabled on this instance.")
		}
		return
	}

	username := s.Actor.Username
	port := portOf(c.opts.SSH.Addr)

	if lang == "zh" {
		fmt.Fprintln(out, "这是 Arc 的终端，不是服务器 shell —— 只能运行这里列出的命令。")
		fmt.Fprintf(out, "连接： ssh %s@<域名> -p %s\n", username, port)
		fmt.Fprintf(out, "主机指纹： %s\n", c.opts.SSH.Fingerprint)
		fmt.Fprintln(out, "单条命令，适合脚本：")
		fmt.Fprintf(out, "  ssh %s@<域名> -p %s 'user list --json' | jq\n", username, port)
	} else {
		fmt.Fprintln(out, "This is the console, not a server shell — only the commands listed here run.")
		fmt.Fprintf(out, "Connect: ssh %s@<host> -p %s\n", username, port)
		fmt.Fprintf(out, "Host fingerprint: %s\n", c.opts.SSH.Fingerprint)
		fmt.Fprintln(out, "One-shot usage, script-friendly:")
		fmt.Fprintf(out, "  ssh %s@<host> -p %s 'user list --json' | jq\n", username, port)
	}
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// renderKeysHelp is `help keys`: the line-editing and terminal shortcuts,
// shared between the web terminal and consolessh's own line editor — the
// key set in the contract's section 2.4 is what this documents.
func renderKeysHelp(out io.Writer, s *Session) {
	type row struct{ key, en, zh string }
	rows := []row{
		{"Enter", "run the line", "执行当前行"},
		{"Shift+Enter", "insert a newline", "插入换行"},
		{"↑ / ↓, Ctrl-P / Ctrl-N", "walk history", "浏览历史命令"},
		{"Ctrl-R", "search history", "反向搜索历史命令"},
		{"Tab", "complete", "自动补全"},
		{"← / →, Ctrl-B / Ctrl-F", "move the cursor", "移动光标"},
		{"Ctrl-A / Home, Ctrl-E / End", "start / end of the line", "跳到行首 / 行尾"},
		{"Ctrl-W", "delete the word to the left", "删除左侧一个单词"},
		{"Ctrl-U / Ctrl-K", "delete to start / end of the line", "删除到行首 / 行尾"},
		{"Ctrl-L", "clear the screen", "清屏"},
		{"Ctrl-C / Esc", "cancel the running command, or abandon the line", "取消正在运行的命令，或放弃当前行"},
		{"Ctrl-D", "end the session on an empty line, delete forward otherwise", "空行时结束会话，否则向前删除一个字符"},
	}

	lang := s.Lang
	if lang == "zh" {
		fmt.Fprintln(out, "快捷键：")
	} else {
		fmt.Fprintln(out, "Keyboard shortcuts:")
	}
	width := 0
	for _, r := range rows {
		if l := len(r.key); l > width {
			width = l
		}
	}
	for _, r := range rows {
		desc := r.en
		if lang == "zh" {
			desc = r.zh
		}
		fmt.Fprintf(out, "  %-*s  %s\n", width, r.key, desc)
	}
}
