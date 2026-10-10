package console

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// The "backup" family is the caller's own data, out and in — the one place
// in this package (so far) where Permission is Anyone rather than an admin
// grant, because /api/account/export and /api/account/import are scoped to
// auth.MustUser(r.Context()) with no id in the path: there is no argument
// that could point a command in this file at a different account, and none
// is accepted. See internal/backup for the document format and why import
// adds rather than replaces.
func init() {
	registerCommand(Command{
		Name:    "backup export",
		Group:   "backup",
		Summary: Text{EN: "Export your account as one JSON document", ZH: "将你的账户导出为一份 JSON 文档"},
		Usage:   "backup export",
		Help: Text{
			EN: "Everything backup import can put back: your preferences and every conversation, " +
				"messages included. Attachment bytes are never kept server-side, so an image is " +
				"counted in the export rather than carried along.",
			ZH: "backup import 能还原的全部内容：你的偏好设置和每一段对话，含全部消息。附件字节本身" +
				"不会在服务端保留，因此图片在导出中只被计数，而不会被一并带走。",
		},
		Examples:   []string{"backup export", "backup export --json > export.json"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/account/export"},
		Run: func(_ context.Context, rt *Runtime) error {
			if _, _, err := rt.Call(http.MethodGet, "/api/account/export", nil); err != nil {
				return err
			}
			// A conversation nests its messages, and the document nests every
			// conversation, so there is no single row a table could key on —
			// flattening it would mean one column of "see below" per thread.
			// The document itself, pretty-printed, is the useful form.
			return RenderJSON(rt.Out, rt.rawJSON)
		},
	})

	registerCommand(Command{
		Name:    "backup import",
		Group:   "backup",
		Summary: Text{EN: "Import a previously exported document into your account", ZH: "将此前导出的文档导入你的账户"},
		Usage:   "backup import <json> --yes",
		Help: Text{
			EN: "The argument is a whole backup export document, pasted as one quoted argument " +
				"(Shift+Enter in the web terminal spreads it over several lines first). Conversations " +
				"are always added as new copies — importing the same file twice leaves you with two " +
				"copies of everything in it, never a merge — but a preference key present in the " +
				"document overwrites your current value for that key.",
			ZH: "参数是一份完整的备份导出文档，作为一个带引号的参数粘贴（网页终端中可先用 Shift+Enter 分行" +
				"输入）。对话总是作为新副本被添加——同一份文件导入两次，其中的内容就会有两份，而不是合并——" +
				"但文档中出现的偏好设置键会覆盖你当前对应键的值。",
		},
		Args: []Arg{{Name: "json", Hint: Text{EN: "the export document", ZH: "导出文档"}, Required: true, Sensitive: true}},
		Examples: []string{
			`backup import '{"obsidian_arc_export":1,"conversations":[]}' --yes`,
			`backup import '{"obsidian_arc_export":1}' -y`,
		},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"POST /api/account/import"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要一份导出文档")
				}
				return rt.Errorf("an export document is required")
			}
			body, err := parseJSONBody(rt, strings.Join(rt.Args(), " "))
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/account/import", body)
			if err != nil {
				return err
			}
			m := asMap(data)
			return rt.Fields([][2]string{
				{"conversations", fmt.Sprint(asNum(m["conversations"]))},
				{"messages", fmt.Sprint(asNum(m["messages"]))},
				{"preferences", fmt.Sprint(asBoolVal(m["preferences"]))},
				{"skipped", fmt.Sprint(asNum(m["skipped"]))},
			})
		},
	})
}
