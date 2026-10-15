package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ---------------------------------------------------------------------
// feedback mine | thread | answer | unread
//
// The author's side of a report, as the feedback panel in the chat shows
// it. The staff side is `feedback list/show/reply`, which is why these are
// not called the same thing: an administrator reading their own report and
// one answering somebody else's are two different speakers on the same
// thread, and the endpoints behind them record which one spoke.
//
// Every route is /api/feedback, scoped to the caller by the handler, so a
// report id belonging to somebody else answers not-found here exactly as it
// does in the panel.
// ---------------------------------------------------------------------

func init() {
	registerCommand(Command{
		Name:       "feedback mine",
		Group:      "feedback",
		Summary:    Text{EN: "List the reports you have sent", ZH: "列出你提交过的反馈"},
		Usage:      "feedback mine",
		Examples:   []string{"feedback mine", "feedback mine --json"},
		SeeAlso:    []string{"feedback thread", "feedback send"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/feedback"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/feedback", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			var rows [][]string
			for _, raw := range asSlice(m["feedback"]) {
				f := asMap(raw)
				unread := ""
				if asBoolVal(f["author_unread"]) {
					unread = "●"
				}
				rows = append(rows, []string{
					asStr(f["id"]), unread, asStr(f["status"]), asStr(f["kind"]),
					truncateForTable(asStr(f["title"]), 40), fmt.Sprint(asNum(f["replies"])), formatMS(f["updated_at"]),
				})
			}
			if err := rt.Table([]string{"id", "new", "status", "kind", "title", "replies", "updated"}, rows); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			// What the panel tells somebody before they write five hundred
			// words into a box they are not allowed to send today.
			if rt.Session.Lang == "zh" {
				rt.Printf("\n今天还能提交 %v 条（每天最多 %v 条）。\n", asNum(m["remaining"]), asNum(m["max_per_day"]))
			} else {
				rt.Printf("\n%v of %v reports left today.\n", asNum(m["remaining"]), asNum(m["max_per_day"]))
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "feedback thread",
		Group:   "feedback",
		Summary: Text{EN: "Read one of your reports and every reply to it", ZH: "查看你的某条反馈及全部回复"},
		Usage:   "feedback thread <feedback-id>",
		Help: Text{
			EN: "Reading a thread marks its replies as read, as opening it in the panel does — that is what clears the dot on the account menu.",
			ZH: "查看一条反馈会把它的回复标为已读，和在面板里打开它一样 —— 账户菜单上的小红点就是这样消掉的。",
		},
		Args:       []Arg{{Name: "feedback-id", Hint: Text{EN: "from feedback mine", ZH: "来自 feedback mine"}, Required: true}},
		Examples:   []string{"feedback thread 01H9Z…", "feedback thread 01H9Z… --json"},
		SeeAlso:    []string{"feedback mine", "feedback answer"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/feedback/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "feedback id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/feedback/"+url.PathEscape(ref), nil)
			if err != nil {
				return err
			}
			thread := asMap(data)
			f := asMap(thread["feedback"])
			if err := rt.Fields([][2]string{
				{"id", asStr(f["id"])}, {"title", asStr(f["title"])}, {"status", asStr(f["status"])},
				{"kind", asStr(f["kind"])}, {"priority", asStr(f["priority"])}, {"created_at", formatMS(f["created_at"])},
			}); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			rt.Printf("\n%s\n", asStr(f["body"]))
			for _, raw := range asSlice(thread["replies"]) {
				reply := asMap(raw)
				who := asStr(reply["nickname"])
				if who == "" {
					who = asStr(reply["username"])
				}
				// A staff reply arrives without a name when the operator
				// has chosen not to show who answered.
				if asBoolVal(reply["from_staff"]) {
					if who == "" && rt.Session.Lang == "zh" {
						who = "管理员"
					} else if who == "" {
						who = "staff"
					}
				} else if rt.Session.Lang == "zh" {
					who = "你"
				} else {
					who = "you"
				}
				rt.Printf("\n— %s · %s\n%s\n", who, formatMS(reply["created_at"]), asStr(reply["body"]))
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "feedback answer",
		Group:   "feedback",
		Summary: Text{EN: "Reply on one of your own reports", ZH: "在你自己的反馈下回复"},
		Usage:   "feedback answer <feedback-id> --body TEXT",
		Help: Text{
			EN: "Adds your turn to the thread, as the reply box in the panel does. It goes through the same endpoint, so an instance that asks for a human check on feedback refuses this too: answer from the panel there.",
			ZH: "在这条反馈的对话里追加你的回复，和面板里的回复框一样。走的是同一个接口，所以站点如果开了提交反馈的人机验证，这条命令也会被拒绝：那时请到面板里回复。",
		},
		Args:       []Arg{{Name: "feedback-id", Hint: Text{EN: "from feedback mine", ZH: "来自 feedback mine"}, Required: true}},
		Flags:      []Flag{{Name: "--body", Hint: Text{EN: "what you want to add, required", ZH: "要补充的内容，必填"}, Value: "TEXT", Sensitive: true}},
		Examples:   []string{`feedback answer 01H9Z… --body "It happens on Firefox too."`, `feedback answer 01H9Z… --body "Fixed, thanks."`},
		SeeAlso:    []string{"feedback thread"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/feedback/{id}/replies"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "feedback id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/feedback/"+url.PathEscape(ref)+"/replies",
				map[string]any{"body": rt.String("body")})
			if err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return rt.Fields([][2]string{{"id", asStr(asMap(data)["id"])}})
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已回复。\n")
			} else {
				rt.Printf("replied.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:       "feedback unread",
		Group:      "feedback",
		Summary:    Text{EN: "Count your reports with replies you have not read", ZH: "统计有未读回复的反馈数"},
		Usage:      "feedback unread",
		Examples:   []string{"feedback unread", "watch --interval 60s feedback unread"},
		SeeAlso:    []string{"feedback mine"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/feedback/unread"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/feedback/unread", nil)
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"unread", fmt.Sprint(asNum(asMap(data)["unread"]))}})
		},
	})
}
