package console

import (
	"context"
	"net/http"
	"net/url"
)

// An account's signed-in devices, from the operator's side — the same shape
// `me sessions` shows the caller about themselves, without a "current"
// column: nothing about the operator's own browser belongs in somebody
// else's list.
func init() {
	registerCommand(Command{
		Name:    "user sessions",
		Group:   "accounts",
		Summary: Text{EN: "List an account's signed-in devices", ZH: "查看账户的登录设备"},
		Usage:   "user sessions <id|username>",
		Help: Text{
			EN: "One row per browser or device holding a real session on the account. " +
				"A password proved with no code yet does not count.",
			ZH: "每一行是该账户上持有真实登录会话的一个浏览器或设备；只完成密码、还没输入验证码的不算。",
		},
		Args:       []Arg{{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true}},
		Examples:   []string{"user sessions alice", "user sessions alice --json"},
		SeeAlso:    []string{"user signout"},
		Permission: "users",
		Endpoints:  []string{"GET /api/admin/users/{id}/sessions"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/users/"+url.PathEscape(uid)+"/sessions", nil)
			if err != nil {
				return err
			}
			rows := make([][]string, 0)
			for _, raw := range asSlice(asMap(data)["sessions"]) {
				s := asMap(raw)
				rows = append(rows, []string{
					asStr(s["id"]), asStr(s["ip"]), asStr(s["user_agent"]),
					formatMS(s["created_at"]), formatMS(s["last_seen_at"]),
				})
			}
			return rt.Table([]string{"id", "ip", "user_agent", "created", "last_seen"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "user signout",
		Group:   "accounts",
		Summary: Text{EN: "Sign an account out of one device, or every device", ZH: "让账户退出某一台设备，或退出全部设备"},
		Usage:   "user signout <id|username> [--session ID] --yes",
		Help: Text{
			EN: "Without --session, ends every session on the account — signing it out " +
				"everywhere at once. With --session ID (an id from `user sessions`), ends " +
				"only that one.",
			ZH: "不加 --session 时，会结束该账户的全部会话，即一次性在所有地方退出登录。" +
				"加上 --session ID（取自 `user sessions` 的会话 id）时，只结束那一个。",
		},
		Args: []Arg{{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true}},
		Flags: []Flag{
			{Name: "--session", Hint: Text{EN: "one session id, from user sessions", ZH: "单个会话 id，来自 user sessions"}, Value: "ID"},
		},
		Examples:    []string{"user signout alice --yes", "user signout alice --session a1b2c3d4e5f60718 --yes"},
		SeeAlso:     []string{"user sessions"},
		Permission:  "users",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/users/{id}/sessions/{sid}", "DELETE /api/admin/users/{id}/sessions"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			path := "/api/admin/users/" + url.PathEscape(uid) + "/sessions"
			if rt.Present("session") {
				path += "/" + url.PathEscape(rt.String("session"))
			}
			if _, _, err := rt.Call(http.MethodDelete, path, nil); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "signed out.", ZH: "已完成退出。"})
		},
	})
}
