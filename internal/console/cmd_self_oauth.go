package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ---------------------------------------------------------------------
// oauth list | unlink
//
// Connecting a provider cannot be done from here: it is a trip to GitHub
// or Google and back through a browser, and there is no browser at the
// other end of an SSH session. Seeing what is connected and removing one
// both can, and removing one is the half that matters over SSH — it is
// what somebody reaches for when a provider account is lost or has been
// taken, at exactly the moment the web interface may be unreachable.
//
// Both routes are /api/auth/oauth/connections, scoped to the caller by the
// handler itself, so Permission: Anyone is correct for the same reason it
// is on `key list`: there is no id here to widen the reach with.
// ---------------------------------------------------------------------

func init() {
	registerCommand(Command{
		Name:    "oauth list",
		Group:   "profile",
		Summary: Text{EN: "List the sign-in providers connected to your account", ZH: "列出已连接到你账户的第三方登录"},
		Usage:   "oauth list",
		Help: Text{
			EN: "'connected' is whether your account answers to that provider; 'offered' is whether " +
				"this server has it switched on at all. A provider that is connected but no longer " +
				"offered will not sign you in — set a password before you rely on it.",
			ZH: "connected 表示你的账户是否已绑定该登录方式；offered 表示这台服务器是否开启了它。" +
				"已绑定但服务器已关闭的登录方式无法再用于登录——在依赖它之前请先设置密码。",
		},
		Examples:   []string{"oauth list", "oauth list --json"},
		SeeAlso:    []string{"oauth unlink", "profile password"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/auth/oauth/connections"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/auth/oauth/connections", nil)
			if err != nil {
				return err
			}
			body := asMap(data)

			connected := map[string]map[string]any{}
			for _, raw := range asSlice(body["connections"]) {
				item := asMap(raw)
				connected[asStr(item["provider"])] = item
			}

			rows := make([][]string, 0, len(asSlice(body["providers"])))
			for _, raw := range asSlice(body["providers"]) {
				provider := asMap(raw)
				id := asStr(provider["id"])
				link, linked := connected[id]
				row := []string{id, asStr(provider["name"]), yesNo(asBoolVal(provider["enabled"])), yesNo(linked), "-", "-"}
				if linked {
					row[4] = asStr(link["login"])
					row[5] = formatMS(link["last_login_at"])
				}
				rows = append(rows, row)
			}
			if err := rt.Table([]string{"id", "name", "offered", "connected", "account", "last_used"}, rows); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			// Whether there is also a password is the fact that decides
			// whether any of these may be removed, so it belongs beside the
			// table rather than inside it.
			hasPassword := yesNo(asBoolVal(body["has_password"]))
			rt.Say(Text{
				EN: fmt.Sprintf("\npassword set: %s\n", hasPassword),
				ZH: fmt.Sprintf("\n已设置密码：%s\n", hasPassword),
			})
			return nil
		},
	})

	registerCommand(Command{
		Name:    "oauth unlink",
		Group:   "profile",
		Summary: Text{EN: "Disconnect a sign-in provider from your account", ZH: "解除你账户与某个第三方登录的绑定"},
		Usage:   "oauth unlink <provider> --yes",
		Help: Text{
			EN: "Reconnecting needs a browser, so this is one-way from here. The server refuses to " +
				"remove the last way into an account: with no password set and one provider " +
				"connected, set a password first.",
			ZH: "重新绑定需要浏览器，所以在这里只能单向解绑。服务器不会移除账户的最后一种登录方式：" +
				"如果你没有设置密码且只绑定了一个登录方式，请先设置密码。",
		},
		Args: []Arg{
			{Name: "provider", Hint: Text{EN: "provider id, from oauth list", ZH: "登录方式 id，来自 oauth list"}, Required: true},
		},
		Examples:    []string{"oauth unlink github --yes", "oauth unlink google -y"},
		SeeAlso:     []string{"oauth list", "profile password"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/auth/oauth/connections/{provider}"},
		Run: func(_ context.Context, rt *Runtime) error {
			provider, err := requireRef(rt, "provider id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete,
				"/api/auth/oauth/connections/"+url.PathEscape(provider), nil); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "disconnected.", ZH: "已解绑。"})
		},
	})
}
