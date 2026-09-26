package console

import (
	"context"
	"net/http"
)

// The "me" family is the caller's own profile — no id in the path, no
// username or account argument accepted anywhere in this file, because
// GET /api/auth/me, PATCH /api/profile, POST /api/profile/password, and
// POST /api/profile/verify/resend and /api/profile/verify/code are scoped to
// the signed-in caller
// (auth.MustUser / auth.UserFrom) with nothing in their request shape that
// could point one at a different account. See cmd_self_backup.go for the
// same shape a level up, over a whole account's data instead of its profile
// fields.
func init() {
	registerCommand(Command{
		Name:    "me show",
		Group:   "profile",
		Summary: Text{EN: "Show your own account", ZH: "显示你自己的账户"},
		Usage:   "me show",
		Help: Text{
			EN: "Username, contact details, role, group and verification state — the same account " +
				"the web client reads from GET /api/auth/me on every page load.",
			ZH: "用户名、联系方式、角色、分组和验证状态——与网页客户端每次加载页面时从 GET /api/auth/me " +
				"读到的账户信息一致。",
		},
		Examples:   []string{"me show", "me show --json"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/auth/me"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/auth/me", nil)
			if err != nil {
				return err
			}
			u := asMap(asMap(data)["user"])

			group := asStr(u["group_name"])
			if group == "" {
				group = "-"
			}

			return rt.Fields([][2]string{
				{"id", asStr(u["id"])},
				{"username", asStr(u["username"])},
				{"nickname", asStr(u["nickname"])},
				{"email", asStr(u["email"])},
				{"email_verified", yesNo(asBoolVal(u["email_verified"]))},
				{"qq", asStr(u["qq"])},
				{"role", asStr(u["role"])},
				{"group", group},
				{"group_expires_at", formatMS(u["group_expires_at"])},
				{"status", asStr(u["status"])},
				{"created_at", formatMS(u["created_at"])},
				{"last_login_at", formatMS(u["last_login_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "me edit",
		Group:   "profile",
		Summary: Text{EN: "Edit your own profile", ZH: "编辑你自己的资料"},
		Usage:   "me edit [flags]",
		Help: Text{
			EN: "Only the flags you give are changed — an absent flag leaves that field alone. When " +
				"this server requires email verification, changing --email marks the new address " +
				"unverified and a confirmation link is sent to it automatically; `me verify` resends " +
				"that link if it does not arrive.",
			ZH: "只会修改你给出的选项，未给出的字段保持不变。当本服务器要求验证邮箱时，修改 --email 会把" +
				"新地址标记为未验证，并自动向其发送一封确认邮件；若没有收到，可用 `me verify` 重新发送。",
		},
		Flags: []Flag{
			{Name: "--nickname", Hint: Text{EN: "display nickname, ≤32 chars", ZH: "昵称，≤32 字符"}, Value: "TEXT"},
			{Name: "--email", Hint: Text{EN: "email address", ZH: "邮箱地址"}, Value: "EMAIL"},
			{Name: "--bio", Hint: Text{EN: "bio, ≤500 chars", ZH: "简介，≤500 字符"}, Value: "TEXT"},
			{Name: "--avatar", Hint: Text{EN: "avatar URL or data, ≤8192 chars", ZH: "头像 URL 或数据，≤8192 字符"}, Value: "TEXT"},
		},
		Examples: []string{
			`me edit --nickname "night owl"`,
			"me edit --email new@example.com --bio 'hello there'",
		},
		Permission: Anyone,
		Endpoints:  []string{"PATCH /api/profile"},
		Run: func(_ context.Context, rt *Runtime) error {
			body := bodyBuilder{}
			body.str(rt, "nickname", "nickname")
			body.str(rt, "email", "email")
			body.str(rt, "bio", "bio")
			body.str(rt, "avatar", "avatar")

			if len(body) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有需要修改的内容：请至少给出一个选项")
				}
				return rt.Errorf("nothing to change: give at least one flag")
			}

			data, _, err := rt.Call(http.MethodPatch, "/api/profile", map[string]any(body))
			if err != nil {
				return err
			}
			u := asMap(asMap(data)["user"])
			return rt.Fields([][2]string{
				{"username", asStr(u["username"])},
				{"nickname", asStr(u["nickname"])},
				{"email", asStr(u["email"])},
				{"email_verified", yesNo(asBoolVal(u["email_verified"]))},
				{"bio", asStr(u["bio"])},
				// The value itself is never echoed back — an avatar can be an
				// 8K data URI, and a terminal is not where anyone wants that
				// printed in full — only whether one is set.
				{"avatar_set", yesNo(asStr(u["avatar"]) != "")},
				{"updated_at", formatMS(u["updated_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "me passwd",
		Group:   "profile",
		Summary: Text{EN: "Change your own password", ZH: "修改你自己的密码"},
		Usage:   "me passwd --current-password OLD --new-password NEW --yes",
		Help: Text{
			EN: "Takes effect immediately and ends every other session on your account — everywhere " +
				"else you are signed in, including any browser tab. A console session is not a " +
				"browser login this command can spare, so only run it somewhere you are prepared to " +
				"sign back in afterwards.",
			ZH: "修改立即生效，并会结束你账户在其他所有地方的登录会话——包括任何浏览器标签页。控制台自身" +
				"的会话并不在这条命令能够保留的范围内，因此请只在你已经准备好重新登录的地方执行它。",
		},
		Flags: []Flag{
			{Name: "--current-password", Hint: Text{EN: "your current password", ZH: "当前密码"}, Value: "OLD", Sensitive: true},
			{Name: "--new-password", Hint: Text{EN: "the new password, 8-256 characters", ZH: "新密码，8-256 字符"}, Value: "NEW", Sensitive: true},
		},
		Examples: []string{
			"me passwd --current-password 'old-one' --new-password 'a-good-password' --yes",
			"me passwd --current-password 'old-one' --new-password 'a-good-password' -y",
		},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"POST /api/profile/password"},
		Run: func(_ context.Context, rt *Runtime) error {
			current := rt.String("current-password")
			next := rt.String("new-password")
			if current == "" || next == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要同时给出 --current-password 和 --new-password")
				}
				return rt.Errorf("both --current-password and --new-password are required")
			}

			if _, _, err := rt.Call(http.MethodPost, "/api/profile/password",
				map[string]any{"current_password": current, "new_password": next}); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("密码已更新，其他会话已全部结束。\n")
			} else {
				rt.Printf("password updated; every other session has been ended.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "me verify",
		Group:   "profile",
		Summary: Text{EN: "Verify your email or resend its link", ZH: "验证邮箱或重新发送验证邮件"},
		Usage:   "me verify [--code CODE]",
		Help: Text{
			EN: "Without --code, sends a fresh confirmation link to your account's email address. " +
				"With --code, submits the six-digit code from that email. Codes expire after ten " +
				"minutes and allow five incorrect attempts. The code is treated as sensitive and " +
				"is omitted from the command audit.",
			ZH: "不带 --code 时，向你账户的邮箱重新发送验证链接；带 --code 时，提交邮件中的六位验证码。" +
				"验证码十分钟后过期，最多可输错五次。验证码属于敏感信息，不会写入命令审计记录。",
		},
		Flags:      []Flag{{Name: "--code", Hint: Text{EN: "six-digit email code", ZH: "六位邮箱验证码"}, Value: "CODE", Sensitive: true}},
		Examples:   []string{"me verify", "me verify --code 123456"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/profile/verify/resend", "POST /api/profile/verify/code"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.Present("code") {
				code := rt.String("code")
				if code == "" {
					if rt.Session.Lang == "zh" {
						return rt.Errorf("--code 不能为空")
					}
					return rt.Errorf("--code cannot be empty")
				}
				if _, _, err := rt.Call(http.MethodPost, "/api/profile/verify/code", map[string]any{"code": code}); err != nil {
					return err
				}
				if rt.Session.Lang == "zh" {
					rt.Printf("邮箱已验证。\n")
				} else {
					rt.Printf("email verified.\n")
				}
				return nil
			}
			if _, _, err := rt.Call(http.MethodPost, "/api/profile/verify/resend", nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("验证邮件已发送。\n")
			} else {
				rt.Printf("verification email sent.\n")
			}
			return nil
		},
	})
}
