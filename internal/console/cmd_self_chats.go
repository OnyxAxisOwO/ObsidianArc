package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// The "chat" family is the first Anyone-tier noun in this package: every
// command here reaches a route mounted under auth.RequireUser, not
// auth.RequireAdmin, and every one of them is scoped to the caller by the
// endpoint itself (internal/conversation.Store never takes a conversation
// id without the owning user id alongside it — see that package's own
// doc comment). So there is no id-or-username argument to resolve here,
// unlike cmd_users.go's account-scoped commands: accepting one would be
// the wrong shape for a command that can only ever mean "mine".
//
// GET /api/conversations takes no offset (internal/chat/http.go's
// listConversations reads only "limit" off the query string), so unlike
// "user chats" there is no --offset flag to declare — one would silently
// do nothing, which is worse than not offering it.
func init() {
	registerCommand(Command{
		Name:    "chat list",
		Group:   "chat",
		Summary: Text{EN: "List your own conversations", ZH: "列出你自己的对话"},
		Usage:   "chat list [--limit N] [--archived] [--project ID]",
		Help: Text{
			EN: "Lists your conversations, pinned first and then newest first.",
			ZH: "列出你的对话，置顶的排在最前，其余按最新排序。",
		},
		Flags: []Flag{
			{Name: "--limit", Hint: Text{EN: "page size, default 60, max 200", ZH: "每页数量，默认 60，最多 200"}, Value: "N", Default: "60"},
			{Name: "--archived", Hint: Text{EN: "show archived conversations", ZH: "显示已归档的对话"}},
			{Name: "--project", Hint: Text{EN: "filter by project id", ZH: "按项目 ID 筛选"}, Value: "ID"},
		},
		Examples:   []string{"chat list", "chat list --limit 20", "chat list --archived", "chat list --project 01H9Z…"},
		SeeAlso:    []string{"chat show", "chat archive", "chat unarchive", "project list"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/conversations"},
		Run: func(_ context.Context, rt *Runtime) error {
			// The server answers an out-of-range limit with its default, 60, and
			// says nothing, so a request for 500 would print 60 rows as if they
			// were the list. Refuse it here instead; the page is 200 at most.
			limit := rt.IntOr("limit", 60)
			if limit < 1 || limit > 200 {
				return rt.Fail(Text{EN: "--limit must be between 1 and 200", ZH: "--limit 必须在 1 到 200 之间"})
			}
			q := url.Values{}
			q.Set("limit", strconv.Itoa(limit))
			if rt.Present("archived") {
				q.Set("archived", strconv.FormatBool(rt.Bool("archived")))
			}
			if rt.Present("project") {
				q.Set("project_id", rt.String("project"))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/conversations?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["conversations"]) {
				c := asMap(raw)
				rows = append(rows, []string{
					asStr(c["id"]), asStr(c["title"]), asStr(c["model_id"]),
					yesNo(asBoolVal(c["pinned"])), yesNo(asBoolVal(c["archived"])),
					fmt.Sprint(asNum(c["message_count"])), formatMS(c["created_at"]),
				})
			}
			return rt.Table([]string{"id", "title", "model_id", "pinned", "archived", "messages", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "chat show",
		Group:   "chat",
		Summary: Text{EN: "Show one of your conversations in full", ZH: "完整显示你的某一段对话"},
		Usage:   "chat show <conversation-id>",
		Args: []Arg{
			{Name: "conversation-id", Hint: Text{EN: "from chat list", ZH: "来自 chat list"}, Required: true},
		},
		Examples:   []string{"chat show 01H9Z…", "chat show 01H9Z… --json"},
		SeeAlso:    []string{"chat list"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/conversations/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "conversation id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/conversations/"+url.PathEscape(ref), nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			c := asMap(m["conversation"])
			jsonMode := rt.effectiveJSON()
			fields := [][2]string{
				{"id", asStr(c["id"])}, {"title", asStr(c["title"])}, {"model_id", asStr(c["model_id"])},
			}
			if pid := asStr(c["project_id"]); pid != "" {
				fields = append(fields, [2]string{"project_id", pid})
			}
			fields = append(fields, [][2]string{
				{"pinned", yesNo(asBoolVal(c["pinned"]))}, {"archived", yesNo(asBoolVal(c["archived"]))},
				{"messages", fmt.Sprint(asNum(c["message_count"]))},
				{"created_at", formatMS(c["created_at"])}, {"updated_at", formatMS(c["updated_at"])},
			}...)
			if err := rt.Fields(fields); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			// Same rendering "user transcript" uses for the admin side of
			// this exact response shape (§ its own comment) — one way to
			// print a transcript, not two.
			fmt.Fprintln(rt.Out, "\nmessages:")
			var rows [][]string
			for _, raw := range asSlice(m["messages"]) {
				msg := asMap(raw)
				stats := asMap(msg["stats"])
				rows = append(rows, []string{
					fmt.Sprint(asNum(msg["seq"])), asStr(msg["role"]), asStr(msg["model_name"]),
					truncateForTable(asStr(msg["content"]), 60),
					fmt.Sprint(asNum(stats["input_tokens"])), fmt.Sprint(asNum(stats["output_tokens"])),
					formatMS(msg["created_at"]),
				})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour,
				[]string{"seq", "role", "model", "content", "in", "out", "created"}, rows)
			return nil
		},
	})

	registerCommand(Command{
		Name:    "chat rename",
		Group:   "chat",
		Summary: Text{EN: "Rename or pin one of your conversations", ZH: "重命名或置顶你的一段对话"},
		Usage:   "chat rename <conversation-id> [--title TEXT] [--pinned BOOL]",
		Help: Text{
			EN: "Only the flags you give are changed — an absent flag leaves that field alone.",
			ZH: "只会修改你给出的选项，未给出的字段保持不变。",
		},
		Args: []Arg{
			{Name: "conversation-id", Hint: Text{EN: "from chat list", ZH: "来自 chat list"}, Required: true},
		},
		Flags: []Flag{
			// Free text the member wrote: the audit keeps the flag, not its value.
			{Name: "--title", Hint: Text{EN: "new title, ≤120 chars", ZH: "新标题，≤120 字符"}, Value: "TEXT", Sensitive: true},
			{Name: "--pinned", Hint: Text{EN: "pin or unpin", ZH: "置顶或取消置顶"}, Value: "BOOL"},
		},
		Examples:   []string{"chat rename 01H9Z… --title \"trip planning\"", "chat rename 01H9Z… --pinned=false"},
		SeeAlso:    []string{"chat list", "chat show"},
		Permission: Anyone,
		Endpoints:  []string{"PATCH /api/conversations/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "conversation id")
			if err != nil {
				return err
			}
			body := bodyBuilder{}
			body.str(rt, "title", "title")
			body.boolv(rt, "pinned", "pinned")
			if len(body) == 0 {
				return rt.Fail(Text{EN: "nothing to change: give at least one flag", ZH: "没有需要修改的内容：请至少给出一个选项"})
			}
			data, _, err := rt.Call(http.MethodPatch, "/api/conversations/"+url.PathEscape(ref), map[string]any(body))
			if err != nil {
				return err
			}
			c := asMap(asMap(data)["conversation"])
			return rt.Fields([][2]string{
				{"id", asStr(c["id"])}, {"title", asStr(c["title"])}, {"model_id", asStr(c["model_id"])},
				{"pinned", yesNo(asBoolVal(c["pinned"]))}, {"updated_at", formatMS(c["updated_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "chat delete",
		Group:   "chat",
		Summary: Text{EN: "Delete one of your conversations", ZH: "删除你的一段对话"},
		Usage:   "chat delete <conversation-id> --yes",
		Help: Text{
			EN: "Deletes the conversation, its messages and its attachments. This cannot be undone.",
			ZH: "会删除该对话及其消息和附件，且无法撤销。",
		},
		Args: []Arg{
			{Name: "conversation-id", Hint: Text{EN: "from chat list", ZH: "来自 chat list"}, Required: true},
		},
		Examples:    []string{"chat delete 01H9Z… --yes", "chat delete 01H9Z… -y"},
		SeeAlso:     []string{"chat list", "chat delete-all"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/conversations/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "conversation id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/conversations/"+url.PathEscape(ref), nil); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "deleted.", ZH: "已删除。"})
		},
	})

	registerCommand(Command{
		Name:    "chat delete-all",
		Group:   "chat",
		Summary: Text{EN: "Delete every conversation you own", ZH: "删除你拥有的全部对话"},
		Usage:   "chat delete-all --yes",
		Help: Text{
			EN: "Deletes every conversation on your account, with their messages and attachments. " +
				"This cannot be undone.",
			ZH: "会删除你账户下的全部对话及其消息和附件，且无法撤销。",
		},
		Examples:    []string{"chat delete-all --yes", "chat delete-all -y"},
		SeeAlso:     []string{"chat delete", "chat list"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/conversations"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodDelete, "/api/conversations", nil)
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"deleted", fmt.Sprint(asNum(asMap(data)["deleted"]))}})
		},
	})

	registerCommand(Command{
		Name:    "chat edit",
		Group:   "chat",
		Summary: Text{EN: "Rewrite one message in one of your conversations", ZH: "改写你某段对话中的一条消息"},
		Usage:   "chat edit <conversation-id> <seq> --content TEXT",
		Help: Text{
			EN: "Replaces the text of the message at that position, as editing it in the chat does. " +
				"It changes the record only: nothing is sent to a model, and the answers after it stay as they were.",
			ZH: "替换该位置那条消息的文字，和在对话里编辑它一样。只改记录：不会再发给模型，后面的回答也保持原样。",
		},
		Args: []Arg{
			{Name: "conversation-id", Hint: Text{EN: "from chat list", ZH: "来自 chat list"}, Required: true},
			{Name: "seq", Hint: Text{EN: "the message's number, from chat show", ZH: "消息序号，来自 chat show"}, Required: true},
		},
		Flags:      []Flag{{Name: "--content", Hint: Text{EN: "the new text, required", ZH: "新的内容，必填"}, Value: "TEXT", Sensitive: true}},
		Examples:   []string{`chat edit 01H9Z… 3 --content "Use Go 1.27, not 1.26."`, `chat edit 01H9Z… 1 --content "Summarise this in English."`},
		SeeAlso:    []string{"chat show"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/conversations/{id}", "PATCH /api/conversations/{id}/messages/{message_id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "conversation id")
			if err != nil {
				return err
			}
			if rt.NArg() < 2 {
				return rt.Fail(Text{EN: "a message number is required (seq, from chat show)", ZH: "需要消息序号（chat show 里的 seq）"})
			}
			// Addressed by the number chat show prints rather than by id:
			// the transcript is what the reader has in front of them, and it
			// does not print ids. The id is looked up from the same response.
			data, _, err := rt.Call(http.MethodGet, "/api/conversations/"+url.PathEscape(ref), nil)
			if err != nil {
				return err
			}
			messageID := ""
			for _, raw := range asSlice(asMap(data)["messages"]) {
				msg := asMap(raw)
				if fmt.Sprint(asNum(msg["seq"])) == rt.Arg(1) {
					messageID = asStr(msg["id"])
				}
			}
			if messageID == "" {
				return rt.Fail(Text{EN: fmt.Sprintf("there is no message %s in this conversation", rt.Arg(1)), ZH: fmt.Sprintf("这段对话里没有序号为 %s 的消息", rt.Arg(1))})
			}
			path := "/api/conversations/" + url.PathEscape(ref) + "/messages/" + url.PathEscape(messageID)
			if _, _, err := rt.Call(http.MethodPatch, path, map[string]any{"content": rt.String("content")}); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "edited.", ZH: "已修改。"})
		},
	})

	registerCommand(Command{
		Name:    "chat archive",
		Group:   "chat",
		Summary: Text{EN: "Archive one of your conversations", ZH: "归档你的一段对话"},
		Usage:   "chat archive <conversation-id>",
		Help: Text{
			EN: "Moves the conversation into the archive without deleting its messages.",
			ZH: "将对话移入归档，但不会删除其中的消息。",
		},
		Args: []Arg{
			{Name: "conversation-id", Hint: Text{EN: "from chat list", ZH: "来自 chat list"}, Required: true},
		},
		Examples:   []string{"chat archive 01H9Z…", "chat archive 01ARZ…"},
		SeeAlso:    []string{"chat unarchive", "chat list"},
		Permission: Anyone,
		Endpoints:  []string{"PATCH /api/conversations/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "conversation id")
			if err != nil {
				return err
			}
			body := map[string]any{"archived": true}
			if _, _, err := rt.Call(http.MethodPatch, "/api/conversations/"+url.PathEscape(ref), body); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "archived.", ZH: "已归档。"})
		},
	})

	registerCommand(Command{
		Name:    "chat unarchive",
		Group:   "chat",
		Summary: Text{EN: "Restore an archived conversation", ZH: "恢复一段已归档的对话"},
		Usage:   "chat unarchive <conversation-id>",
		Help: Text{
			EN: "Restores an archived conversation back to your active chat list.",
			ZH: "将已归档的对话恢复到活动对话列表中。",
		},
		Args: []Arg{
			{Name: "conversation-id", Hint: Text{EN: "from chat list --archived", ZH: "来自 chat list --archived"}, Required: true},
		},
		Examples:   []string{"chat unarchive 01H9Z…", "chat unarchive 01ARZ…"},
		SeeAlso:    []string{"chat archive", "chat list"},
		Permission: Anyone,
		Endpoints:  []string{"PATCH /api/conversations/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "conversation id")
			if err != nil {
				return err
			}
			body := map[string]any{"archived": false}
			if _, _, err := rt.Call(http.MethodPatch, "/api/conversations/"+url.PathEscape(ref), body); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "unarchived.", ZH: "已取消归档。"})
		},
	})
}
