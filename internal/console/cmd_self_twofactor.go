package console

import (
	"context"
	"net/http"
	"strconv"
)

// The "2fa" family is the caller's own second sign-in step, over the same
// endpoints the security settings screen calls. Enrolling works here too:
// there is no picture to scan, but every authenticator app also accepts the
// secret typed in by hand, and `2fa setup` prints it.
func init() {
	registerCommand(Command{
		Name:    "2fa status",
		Group:   "profile",
		Summary: Text{EN: "Show your two-step sign-in", ZH: "查看你的两步验证状态"},
		Usage:   "2fa status",
		Help: Text{
			EN: "Whether signing in asks for a code as well as the password, how many recovery codes " +
				"are left, and whether this server requires it of your account.",
			ZH: "登录时是否在密码之外还要求验证码、还剩多少个恢复码，以及本服务器是否要求你的账户启用它。",
		},
		Examples:   []string{"2fa status", "2fa status --json"},
		SeeAlso:    []string{"2fa setup", "2fa disable"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/profile/two-factor"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/profile/two-factor", nil)
			if err != nil {
				return err
			}
			s := asMap(data)
			return rt.Fields([][2]string{
				{"enabled", yesNo(asBoolVal(s["enabled"]))},
				{"enabled_at", formatMS(s["enabled_at"])},
				{"recovery_remaining", strconv.FormatInt(asInt64Val(s["recovery_remaining"]), 10)},
				{"required", yesNo(asBoolVal(s["mandatory"]))},
				{"policy", asStr(s["policy"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "2fa setup",
		Group:   "profile",
		Summary: Text{EN: "Start switching on two-step sign-in", ZH: "开始启用两步验证"},
		Usage:   "2fa setup",
		Help: Text{
			EN: "Prints a new secret. Add it to an authenticator app by hand (choose \"enter a setup " +
				"key\", time-based), then confirm with `2fa enable CODE` using the code the app shows. " +
				"Nothing changes until you confirm, and running this again replaces the secret.",
			ZH: "打印一个新的密钥。在身份验证器应用中选择“输入设置密钥”（基于时间）手动添加，然后用应用显示的" +
				"验证码执行 `2fa enable 验证码` 完成确认。确认之前不会有任何改变，再次执行会换一个新密钥。",
		},
		Examples:   []string{"2fa setup", "2fa setup --json"},
		SeeAlso:    []string{"2fa enable"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/profile/two-factor/setup"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodPost, "/api/profile/two-factor/setup", nil)
			if err != nil {
				return err
			}
			s := asMap(data)
			return rt.Fields([][2]string{
				{"issuer", asStr(s["issuer"])},
				{"account", asStr(s["account"])},
				{"secret", asStr(s["secret"])},
				{"uri", asStr(s["uri"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "2fa enable",
		Group:   "profile",
		Summary: Text{EN: "Confirm and switch on two-step sign-in", ZH: "确认并启用两步验证"},
		Usage:   "2fa enable <code> [--current-password PASSWORD] --yes",
		Help: Text{
			EN: "Confirms the secret from `2fa setup` with a code from your app. Prints ten recovery " +
				"codes, once: keep them somewhere other than the phone. Every other session on your " +
				"account is ended. An account with a password must give it with --current-password.",
			ZH: "用应用中的验证码确认 `2fa setup` 给出的密钥。会打印十个恢复码，仅显示这一次：请保存在手机" +
				"以外的地方。你账户在其他地方的登录会话都会被结束。设有密码的账户须用 --current-password 给出密码。",
		},
		Args: []Arg{{Name: "code", Hint: Text{EN: "the six digits your app shows", ZH: "应用显示的六位数字"}, Required: true, Sensitive: true}},
		Flags: []Flag{
			{Name: "--current-password", Hint: Text{EN: "your current password, if the account has one", ZH: "当前密码（账户设有密码时需要）"}, Value: "PASSWORD", Sensitive: true},
		},
		Examples:    []string{"2fa enable 123456 --current-password 'my-password' --yes", "2fa enable '123 456' -y"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"POST /api/profile/two-factor/enable"},
		Run: func(_ context.Context, rt *Runtime) error {
			code, err := requireRef(rt, "code")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/profile/two-factor/enable",
				map[string]any{"code": code, "current_password": rt.String("current-password")})
			if err != nil {
				return err
			}
			return printRecoveryCodes(rt, asMap(data)["recovery_codes"])
		},
	})

	registerCommand(Command{
		Name:    "2fa disable",
		Group:   "profile",
		Summary: Text{EN: "Switch off two-step sign-in", ZH: "关闭两步验证"},
		Usage:   "2fa disable <code> --yes",
		Help: Text{
			EN: "Takes a code from your app, or one of your recovery codes. Refused where this " +
				"server requires two-step sign-in for your account.",
			ZH: "需要应用中的验证码，或一个恢复码。若本服务器要求你的账户启用两步验证，则会被拒绝。",
		},
		Args:        []Arg{{Name: "code", Hint: Text{EN: "a code from your app, or a recovery code", ZH: "应用中的验证码，或恢复码"}, Required: true, Sensitive: true}},
		Examples:    []string{"2fa disable 123456 --yes", "2fa disable abcde-fghjk -y"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"POST /api/profile/two-factor/disable"},
		Run: func(_ context.Context, rt *Runtime) error {
			code, err := requireRef(rt, "code")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodPost, "/api/profile/two-factor/disable", map[string]any{"code": code}); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("两步验证已关闭。\n")
			} else {
				rt.Printf("two-step sign-in is off.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "2fa recovery",
		Group:   "profile",
		Summary: Text{EN: "Replace your recovery codes", ZH: "重新生成恢复码"},
		Usage:   "2fa recovery <code> --yes",
		Help: Text{
			EN: "Every unused recovery code stops working and ten new ones are printed, once.",
			ZH: "所有未使用的恢复码都会失效，并打印十个新的恢复码，仅显示这一次。",
		},
		Args:        []Arg{{Name: "code", Hint: Text{EN: "a code from your app, or a recovery code", ZH: "应用中的验证码，或恢复码"}, Required: true, Sensitive: true}},
		Examples:    []string{"2fa recovery 123456 --yes", "2fa recovery 123456 --yes --json"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"POST /api/profile/two-factor/recovery"},
		Run: func(_ context.Context, rt *Runtime) error {
			code, err := requireRef(rt, "code")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/profile/two-factor/recovery", map[string]any{"code": code})
			if err != nil {
				return err
			}
			return printRecoveryCodes(rt, asMap(data)["recovery_codes"])
		},
	})
}

func init() {
	registerCommand(Command{
		Name:    "2fa backoffice",
		Group:   "profile",
		Summary: Text{EN: "Unlock the administrative commands with a code", ZH: "输入验证码以使用管理命令"},
		Usage:   "2fa backoffice <code>",
		Help: Text{
			EN: "Where the server asks for a code at the backoffice's door, the terminal's administrative " +
				"commands ask too, and this is where it is typed. In the web terminal it counts for this " +
				"browser, the backoffice pages included; over SSH it counts for this connection until it " +
				"hangs up. How long it lasts is the server's setting.",
			ZH: "当服务器要求进入管理后台时输入验证码，终端里的管理命令同样需要，验证码就在这里输入。在网页终端中，" +
				"它对当前浏览器有效（包括后台页面）；通过 SSH 时，它对当前连接有效，断开即失效。有效多久由服务器设置决定。",
		},
		Args:       []Arg{{Name: "code", Hint: Text{EN: "a code from your app, or a recovery code", ZH: "应用中的验证码，或恢复码"}, Required: true, Sensitive: true}},
		Examples:   []string{"2fa backoffice 123456", "2fa backoffice abcde-fghjk"},
		SeeAlso:    []string{"2fa status"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/profile/two-factor/backoffice"},
		Run: func(_ context.Context, rt *Runtime) error {
			code, err := requireRef(rt, "code")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodPost, "/api/profile/two-factor/backoffice", map[string]any{"code": code}); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("后台命令已解锁。\n")
			} else {
				rt.Printf("backoffice commands unlocked.\n")
			}
			return nil
		},
	})
}

func printRecoveryCodes(rt *Runtime, raw any) error {
	var rows [][]string
	for _, code := range asSlice(raw) {
		rows = append(rows, []string{asStr(code)})
	}
	if rt.Session.Lang == "zh" {
		rt.Printf("恢复码（仅显示这一次，每个只能用一次）：\n")
	} else {
		rt.Printf("recovery codes (shown once; each works once):\n")
	}
	return rt.Table([]string{"recovery_code"}, rows)
}
