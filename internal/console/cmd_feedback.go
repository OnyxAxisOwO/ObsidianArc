package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The "feedback" family, split across both tiers on purpose.
//
// Reading, answering, resolving and deleting are the operator's half and carry the
// "feedback" grant, exactly as the backoffice page does. Sending one is
// `feedback send`, an Anyone-tier command reaching POST /api/feedback — the
// same route the panel in the chat posts to, so a report typed at the
// console is a report from the account that typed it, with the same daily
// cap and the same validation. There is deliberately no way here to file a
// report as somebody else: a report is a thing a person said.
func init() {
	registerCommand(Command{
		Name:    "feedback list",
		Group:   "feedback",
		Summary: Text{EN: "List feedback from users", ZH: "列出用户反馈"},
		Usage:   "feedback list [--status open|resolved] [--kind bug|idea] [--priority low|medium|high] [--q TEXT] [--limit N]",
		Help: Text{
			EN: "Lists reports newest first. Without --status, open and resolved are both listed; the summary line at the end counts what is still waiting.",
			ZH: "按时间倒序列出反馈。不带 --status 时未处理和已处理都会列出；末尾的汇总行统计还没处理的数量。",
		},
		Flags: []Flag{
			{Name: "--status", Hint: Text{EN: "open or resolved", ZH: "open 或 resolved"}, Value: "STATUS"},
			{Name: "--kind", Hint: Text{EN: "bug or idea", ZH: "bug 或 idea"}, Value: "KIND"},
			{Name: "--priority", Hint: Text{EN: "low, medium or high", ZH: "low、medium 或 high"}, Value: "PRIORITY"},
			{Name: "--q", Hint: Text{EN: "search title, body and author", ZH: "搜索标题、正文与作者"}, Value: "TEXT"},
			{Name: "--limit", Hint: Text{EN: "page size, default 20", ZH: "每页数量，默认 20"}, Value: "N"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N"},
		},
		Examples: []string{
			"feedback list",
			"feedback list --status open --priority high",
			"feedback list --kind bug --q upload",
		},
		SeeAlso:    []string{"feedback show", "feedback resolve"},
		Permission: "feedback",
		Endpoints:  []string{"GET /api/admin/feedback"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/feedback?"+feedbackQuery(rt).Encode(), nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			var rows [][]string
			for _, raw := range asSlice(m["feedback"]) {
				f := asMap(raw)
				rows = append(rows, []string{
					asStr(f["id"]), asStr(f["kind"]), asStr(f["priority"]), asStr(f["status"]),
					asStr(f["username"]), truncateForTable(asStr(f["title"]), 40),
					formatMS(f["created_at"]),
				})
			}
			if err := rt.Table([]string{"id", "kind", "priority", "status", "from", "title", "created"}, rows); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			summary := asMap(m["summary"])
			if rt.Session.Lang == "zh" {
				rt.Printf("\n共 %v 条，未处理 %v 条（其中高优先级 %v 条）；本页 %d 条。\n",
					asNum(m["total"]), asNum(summary["open"]), asNum(summary["high_open"]), len(rows))
			} else {
				rt.Printf("\n%v matching, %v still open (%v of them high priority); %d on this page.\n",
					asNum(m["total"]), asNum(summary["open"]), asNum(summary["high_open"]), len(rows))
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "feedback show",
		Group:   "feedback",
		Summary: Text{EN: "Show one report and its whole conversation", ZH: "显示一条反馈及其全部往来"},
		Usage:   "feedback show <feedback-id>",
		Help: Text{
			EN: "Shows the report as it was written, then every reply in the order they were said. Opening a thread here counts as having read it, exactly as it does in the backoffice, so the \"waiting for an answer\" mark clears.",
			ZH: "先显示反馈原文，再按时间顺序显示全部回复。在这里打开一条，和在后台打开一样算作已读，「待回复」标记会随之清除。",
		},
		Args:       []Arg{{Name: "feedback-id", Hint: Text{EN: "from feedback list", ZH: "来自 feedback list"}, Required: true}},
		Examples:   []string{"feedback show 01H9Z…", "feedback show 01H9Z… --json"},
		SeeAlso:    []string{"feedback list", "feedback reply"},
		Permission: "feedback",
		Endpoints:  []string{"GET /api/admin/feedback/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "feedback id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/feedback/"+url.PathEscape(ref), nil)
			if err != nil {
				return err
			}
			thread := asMap(data)
			found := asMap(thread["feedback"])
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"id", asStr(found["id"])},
				{"kind", asStr(found["kind"])},
				{"priority", asStr(found["priority"])},
				{"status", asStr(found["status"])},
				{"from", asStr(found["username"])},
				{"user_id", asStr(found["user_id"])},
				{"title", asStr(found["title"])},
				{"replies", fmt.Sprint(asNum(found["replies"]))},
				{"created_at", formatMS(found["created_at"])},
				{"updated_at", formatMS(found["updated_at"])},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}

			// Printf, not Fprintln to rt.Out: the body is a stranger's text
			// and Printf is what strips an escape sequence out of it.
			rt.Printf("\n%s\n", asStr(found["body"]))

			// Markdown as it was typed, not rendered: the terminal is not a
			// browser, and the source is what somebody would paste back into
			// an issue anyway.
			for _, raw := range asSlice(thread["replies"]) {
				reply := asMap(raw)
				who := asStr(reply["username"])
				if asBoolVal(reply["from_staff"]) {
					who += " (staff)"
				}
				rt.Printf("\n--- %s · %s · %s\n%s\n",
					who, formatMS(reply["created_at"]), asStr(reply["id"]), asStr(reply["body"]))
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "feedback resolve",
		Group:   "feedback",
		Summary: Text{EN: "Mark a report dealt with", ZH: "把反馈标记为已处理"},
		Usage:   "feedback resolve <feedback-id>",
		Help: Text{
			EN: "Sets the status to resolved. Reversible with `feedback reopen`; the report itself is never edited.",
			ZH: "把状态设为 resolved。可以用 `feedback reopen` 撤回；反馈正文永远不会被改动。",
		},
		Args:       []Arg{{Name: "feedback-id", Hint: Text{EN: "from feedback list", ZH: "来自 feedback list"}, Required: true}},
		Examples:   []string{"feedback resolve 01H9Z…", "feedback resolve 01H9Z… --json"},
		SeeAlso:    []string{"feedback reopen", "feedback list"},
		Permission: "feedback",
		Endpoints:  []string{"PATCH /api/admin/feedback/{id}"},
		Run:        setFeedbackStatus("resolved"),
	})

	registerCommand(Command{
		Name:       "feedback reopen",
		Group:      "feedback",
		Summary:    Text{EN: "Put a report back on the pile", ZH: "把反馈重新打开"},
		Usage:      "feedback reopen <feedback-id>",
		Help:       Text{EN: "Sets the status back to open.", ZH: "把状态改回 open。"},
		Args:       []Arg{{Name: "feedback-id", Hint: Text{EN: "from feedback list", ZH: "来自 feedback list"}, Required: true}},
		Examples:   []string{"feedback reopen 01H9Z…", "feedback reopen 01H9Z… --json"},
		SeeAlso:    []string{"feedback resolve"},
		Permission: "feedback",
		Endpoints:  []string{"PATCH /api/admin/feedback/{id}"},
		Run:        setFeedbackStatus("open"),
	})

	registerCommand(Command{
		Name:    "feedback reply",
		Group:   "feedback",
		Summary: Text{EN: "Answer a report", ZH: "回复一条反馈"},
		Usage:   `feedback reply <feedback-id> --text "…"`,
		Help: Text{
			EN: "Writes a reply as an operator. Markdown, the same as the panel — it is rendered for the reader by the same renderer the transcript uses. The author sees it on their own feedback panel and gets a mark on their account menu; replying does not change the status, because answering something and being finished with it are different claims.",
			ZH: "以管理员身份回复。内容是 Markdown，和面板里一样，展示给用户时用的是和对话界面同一个渲染器。提交人会在自己的反馈面板里看到，账户菜单上也会出现提示；回复不会改状态——回答了和处理完了是两回事。",
		},
		Args: []Arg{{Name: "feedback-id", Hint: Text{EN: "from feedback list", ZH: "来自 feedback list"}, Required: true}},
		Flags: []Flag{
			{Name: "--text", Hint: Text{EN: "what to say, Markdown", ZH: "回复内容，支持 Markdown"}, Value: "TEXT"},
		},
		Examples: []string{
			`feedback reply 01H9Z… --text "Fixed in v2026.09.20, thank you."`,
			`feedback reply 01H9Z… --text "Which model were you using?"`,
		},
		SeeAlso:    []string{"feedback show", "feedback resolve"},
		Permission: "feedback",
		Endpoints:  []string{"POST /api/admin/feedback/{id}/replies"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "feedback id")
			if err != nil {
				return err
			}
			text := rt.String("text")
			if strings.TrimSpace(text) == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --text 写点什么。")
				}
				return rt.Errorf("--text is required")
			}
			data, _, err := rt.Call(http.MethodPost,
				"/api/admin/feedback/"+url.PathEscape(ref)+"/replies",
				map[string]any{"body": text})
			if err != nil {
				return err
			}
			reply := asMap(data)
			return rt.Fields([][2]string{
				{"id", asStr(reply["id"])},
				{"feedback_id", asStr(reply["feedback_id"])},
				{"created_at", formatMS(reply["created_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "feedback reply delete",
		Group:   "feedback",
		Summary: Text{EN: "Delete one reply from a thread", ZH: "删除会话里的某一条回复"},
		Usage:   "feedback reply delete <feedback-id> <reply-id> --yes",
		Help: Text{
			EN: "Removes a single turn, for spam inside a thread that is otherwise worth keeping. `feedback delete` is the blunter answer that takes the whole report with it.",
			ZH: "只删掉其中一条，用于一条本身值得保留的会话里混进来的垃圾内容。要连整条反馈一起删，用 `feedback delete`。",
		},
		Args: []Arg{
			{Name: "feedback-id", Hint: Text{EN: "from feedback list", ZH: "来自 feedback list"}, Required: true},
			{Name: "reply-id", Hint: Text{EN: "from feedback show", ZH: "来自 feedback show"}, Required: true},
		},
		Examples:    []string{"feedback reply delete 01H9Z… 01H9A… --yes", "feedback reply delete 01H9Z… 01H9A… -y"},
		SeeAlso:     []string{"feedback show", "feedback delete"},
		Permission:  "feedback",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/feedback/{id}/replies/{reply}"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() < 2 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要反馈 id 和回复 id。")
				}
				return rt.Errorf("a feedback id and a reply id are required")
			}
			path := "/api/admin/feedback/" + url.PathEscape(rt.Arg(0)) + "/replies/" + url.PathEscape(rt.Arg(1))
			if _, _, err := rt.Call(http.MethodDelete, path, nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已删除回复 %s。\n", rt.Arg(1))
			} else {
				rt.Printf("deleted reply %s.\n", rt.Arg(1))
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "feedback delete",
		Group:   "feedback",
		Summary: Text{EN: "Delete a report", ZH: "删除一条反馈"},
		Usage:   "feedback delete <feedback-id> --yes",
		Help: Text{
			EN: "Removes the report entirely. Resolving is what you usually want — this is for spam.",
			ZH: "彻底删除这条反馈。通常你要的是 resolve，删除是留给垃圾内容的。",
		},
		Args:        []Arg{{Name: "feedback-id", Hint: Text{EN: "from feedback list", ZH: "来自 feedback list"}, Required: true}},
		Examples:    []string{"feedback delete 01H9Z… --yes", "feedback delete 01H9Z… -y"},
		SeeAlso:     []string{"feedback resolve"},
		Permission:  "feedback",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/feedback/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "feedback id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/feedback/"+url.PathEscape(ref), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已删除 %s。\n", ref)
			} else {
				rt.Printf("deleted %s.\n", ref)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "feedback send",
		Group:   "feedback",
		Summary: Text{EN: "Send feedback of your own", ZH: "提交你自己的反馈"},
		Usage:   "feedback send --title TEXT --body TEXT [--kind bug|idea] [--priority low|medium|high]",
		Help: Text{
			EN: "Files a report from your own account, exactly as the panel in the chat does — same daily limit, same validation. There is no way to file one as somebody else. It reaches the same endpoint, so an instance with the human check switched on for feedback refuses this command too: send from the panel there.",
			ZH: "以你自己的账户提交反馈，和对话界面里的反馈面板完全一样 —— 同样的每日上限、同样的校验。没有办法以别人的身份提交。走的是同一个接口，所以站点如果开了提交反馈的人机验证，这条命令也会被拒绝：那时候请到面板里提交。",
		},
		Flags: []Flag{
			{Name: "--title", Hint: Text{EN: "one line, required", ZH: "一行标题，必填"}, Value: "TEXT"},
			{Name: "--body", Hint: Text{EN: "what happened, required", ZH: "具体内容，必填"}, Value: "TEXT"},
			{Name: "--kind", Hint: Text{EN: "bug (default) or idea", ZH: "bug（默认）或 idea"}, Value: "KIND"},
			{Name: "--priority", Hint: Text{EN: "low, medium (default) or high", ZH: "low、medium（默认）或 high"}, Value: "PRIORITY"},
		},
		Examples: []string{
			`feedback send --title "Upload fails over 8 MB" --body "413 from /api/attachments"`,
			`feedback send --kind idea --priority low --title "Dark terminal theme" --body "…"`,
		},
		SeeAlso:    []string{"feedback list"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/feedback"},
		Run: func(_ context.Context, rt *Runtime) error {
			payload := map[string]any{
				"title":    rt.String("title"),
				"body":     rt.String("body"),
				"kind":     rt.StringOr("kind", "bug"),
				"priority": rt.StringOr("priority", "medium"),
			}
			data, _, err := rt.Call(http.MethodPost, "/api/feedback", payload)
			if err != nil {
				return err
			}
			f := asMap(data)
			if rt.effectiveJSON() {
				return rt.Fields([][2]string{{"id", asStr(f["id"])}})
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已提交，编号 %s。\n", asStr(f["id"]))
			} else {
				rt.Printf("sent, id %s.\n", asStr(f["id"]))
			}
			return nil
		},
	})
}

// feedbackQuery builds the list endpoint's query string from the flags the
// list command declares, leaving out anything the caller did not ask for so
// the server's own defaults apply.
func feedbackQuery(rt *Runtime) url.Values {
	q := url.Values{}
	for _, name := range []string{"status", "kind", "priority", "q"} {
		if value := rt.String(name); value != "" {
			q.Set(name, value)
		}
	}
	q.Set("limit", strconv.Itoa(rt.IntOr("limit", 20)))
	if offset := rt.IntOr("offset", 0); offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return q
}

// setFeedbackStatus is the body `feedback resolve` and `feedback reopen`
// share: the same call with the one word that differs.
func setFeedbackStatus(status string) func(context.Context, *Runtime) error {
	return func(_ context.Context, rt *Runtime) error {
		ref, err := requireRef(rt, "feedback id")
		if err != nil {
			return err
		}
		data, _, err := rt.Call(http.MethodPatch, "/api/admin/feedback/"+url.PathEscape(ref),
			map[string]any{"status": status})
		if err != nil {
			return err
		}
		f := asMap(data)
		return rt.Fields([][2]string{
			{"id", asStr(f["id"])},
			{"status", asStr(f["status"])},
			{"title", asStr(f["title"])},
		})
	}
}
