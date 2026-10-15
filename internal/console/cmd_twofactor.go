package console

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Two-step sign-in from the operator's side: how many accounts have it, and
// the reset for somebody who lost their phone and their recovery codes.
func init() {
	registerCommand(Command{
		Name:    "security two-factor",
		Group:   "instance",
		Summary: Text{EN: "Show two-step sign-in adoption", ZH: "查看两步验证的启用情况"},
		Usage:   "security two-factor",
		Help: Text{
			EN: "The policy in force, how many active accounts and administrators have two-step " +
				"sign-in, and the administrators who do not yet. The policy itself is the setting " +
				"security.two_factor_policy: optional, backoffice, admins or everyone.",
			ZH: "当前策略、启用了两步验证的活跃账户与管理员数量，以及尚未启用的管理员。策略本身是设置项 " +
				"security.two_factor_policy：optional、backoffice、admins 或 everyone。",
		},
		Examples:   []string{"security two-factor", "security two-factor --json"},
		SeeAlso:    []string{"user reset-2fa", "setting set"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/security/two-factor"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/security/two-factor", nil)
			if err != nil {
				return err
			}
			a := asMap(data)
			count := func(key string) string { return strconv.FormatInt(asInt64Val(a[key]), 10) }
			if err := rt.Fields([][2]string{
				{"policy", asStr(a["policy"])},
				{"accounts", count("enabled") + " / " + count("accounts")},
				{"administrators", count("admins_enabled") + " / " + count("admins")},
				{"remember_days", count("remember_days")},
			}); err != nil {
				return err
			}
			// Under --json the Fields call above has printed the whole response.
			// The table below would print it a second time.
			if rt.effectiveJSON() {
				return nil
			}
			var rows [][]string
			for _, raw := range asSlice(a["admins_without"]) {
				m := asMap(raw)
				rows = append(rows, []string{asStr(m["id"]), asStr(m["username"]), asStr(m["nickname"])})
			}
			if len(rows) == 0 {
				return nil
			}
			return rt.Table([]string{"administrator without two-step", "username", "nickname"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "user reset-2fa",
		Group:   "accounts",
		Summary: Text{EN: "Switch off an account's two-step sign-in", ZH: "关闭某账户的两步验证"},
		Usage:   "user reset-2fa <id|username> --yes",
		Help: Text{
			EN: "For somebody who has lost both their authenticator and their recovery codes. They " +
				"can then sign in with the password alone — and, where the policy requires it, are " +
				"asked to set it up again straight away. Make sure it is really them asking.",
			ZH: "用于同时丢失了身份验证器和恢复码的用户。之后对方仅凭密码即可登录——若策略要求，会被立即" +
				"要求重新设置。请先确认提出请求的确实是账户本人。",
		},
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
		},
		Examples:    []string{"user reset-2fa alice --yes", "user reset-2fa 01ARZ3NDEKTSV4RRFFQ69G5FAV -y"},
		Permission:  "users",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/users/{id}/two-factor"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/users/"+url.PathEscape(uid)+"/two-factor", nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已关闭该账户的两步验证。\n")
			} else {
				rt.Printf("two-step sign-in is off for that account.\n")
			}
			return nil
		},
	})
}
