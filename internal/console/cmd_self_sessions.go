package console

import (
	"context"
	"net/http"
	"net/url"
)

// The caller's own "signed-in devices": the same list the security settings
// screen shows, over the same endpoints. Every route here is /api/profile,
// never /api/admin/*, so Permission: Anyone is correct the way it is in
// cmd_self_keys.go — an account can only ever name its own sessions.
func init() {
	registerCommand(Command{
		Name:    "me sessions",
		Group:   "profile",
		Summary: Text{EN: "List where your account is signed in", ZH: "查看你的账户在哪些地方登录"},
		Usage:   "me sessions",
		Help: Text{
			EN: "One row per browser or device holding a real session — a password " +
				"proved with no code yet does not count. \"this session\" marks the " +
				"one this command itself is running over.",
			ZH: "每一行是一个持有真实登录会话的浏览器或设备——只完成密码、还没输入验证码的不算。" +
				"“this session” 标记的正是执行这条命令所用的那个会话。",
		},
		Examples:   []string{"me sessions", "me sessions --json"},
		SeeAlso:    []string{"me signout"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/profile/sessions"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/profile/sessions", nil)
			if err != nil {
				return err
			}
			rows := make([][]string, 0)
			for _, raw := range asSlice(asMap(data)["sessions"]) {
				s := asMap(raw)
				current := ""
				if asBoolVal(s["current"]) {
					current = "this session"
				}
				rows = append(rows, []string{
					asStr(s["id"]), asStr(s["ip"]), asStr(s["user_agent"]),
					formatMS(s["created_at"]), formatMS(s["last_seen_at"]), current,
				})
			}
			return rt.Table([]string{"id", "ip", "user_agent", "created", "last_seen", "current"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "me signout",
		Group:   "profile",
		Summary: Text{EN: "Sign out one of your other devices, or all of them", ZH: "退出你的某一台其他设备，或全部退出"},
		Usage:   "me signout (<id> | --others) --yes",
		Help: Text{
			EN: "<id> is a session's id from `me sessions`; that session ends immediately. " +
				"You cannot sign out this session with it — that is what `exit` is for. " +
				"--others ends every session but this one in one call, for when a device " +
				"you no longer hold might still be signed in.",
			ZH: "<id> 取自 `me sessions` 中的会话 id，该会话会立即结束；不能用它退出正在执行这条命令" +
				"本身的会话——那是 `exit` 的作用。--others 一次性结束除本次会话外的所有会话，" +
				"适用于担心某台已不在手边的设备仍处于登录状态的情况。",
		},
		Args: []Arg{
			{Name: "id", Hint: Text{EN: "a session id from me sessions, or omit with --others", ZH: "来自 me sessions 的会话 id；使用 --others 时可省略"}},
		},
		Flags: []Flag{
			{Name: "--others", Hint: Text{EN: "sign out every session but this one", ZH: "退出除本次会话外的全部会话"}},
		},
		Examples:    []string{"me signout a1b2c3d4e5f60718 --yes", "me signout --others -y"},
		SeeAlso:     []string{"me sessions"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/profile/sessions/{id}", "POST /api/profile/sessions/revoke-others"},
		Run: func(_ context.Context, rt *Runtime) error {
			// Bool, not Present: "--others false" is given, and must not sign out every other device.
			if rt.Bool("others") {
				// The branch below never reads an id, so one given beside --others would be
				// dropped without a word. The line is refused instead.
				if rt.NArg() > 0 {
					if rt.Session.Lang == "zh" {
						return rt.Errorf("会话 id 与 --others 只能给一个")
					}
					return rt.Errorf("give a session id or --others, not both")
				}
				if _, _, err := rt.Call(http.MethodPost, "/api/profile/sessions/revoke-others", nil); err != nil {
					return err
				}
				if rt.Session.Lang == "zh" {
					rt.Printf("已退出其他设备。\n")
				} else {
					rt.Printf("signed out of your other devices.\n")
				}
				return nil
			}
			sessionID, err := requireRef(rt, "session id (or --others)")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/profile/sessions/"+url.PathEscape(sessionID), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已退出该设备。\n")
			} else {
				rt.Printf("signed out of that device.\n")
			}
			return nil
		},
	})
}
