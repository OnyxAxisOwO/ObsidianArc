package console

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------
// app list | show | create | edit | secret | delete
//
// The applications allowed to use this instance as a sign-in. Every route
// here is mounted with the security grant in admin.go, so every command
// declares that grant verbatim — the parity tests check the two against
// each other rather than trusting either.
//
// The client secret appears exactly once, in the output of `app create` or
// `app secret`. Nothing stores it and nothing can print it again.
// ---------------------------------------------------------------------

func appRows(data any) []map[string]any {
	items := asSlice(asMap(data)["applications"])
	out := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		out = append(out, asMap(raw))
	}
	return out
}

func joinStrings(value any, separator string) string {
	items := asSlice(value)
	if len(items) == 0 {
		return "-"
	}
	parts := make([]string, len(items))
	for i, raw := range items {
		parts[i] = asStr(raw)
	}
	return strings.Join(parts, separator)
}

func init() {
	registerCommand(Command{
		Name:    "app list",
		Group:   "instance",
		Summary: Text{EN: "List the applications that may sign people in with this instance", ZH: "列出可以用本站账号登录的应用"},
		Usage:   "app list",
		Help: Text{
			EN: "These are the other sites that offer a \"sign in with this instance\" button. The " +
				"issuer printed underneath is what their configuration calls this server; most " +
				"software needs only that, and finds the rest through the discovery document.",
			ZH: "这些是在自己页面上提供「用本站账号登录」的其他站点。下方打印的 issuer 就是它们配置里填的" +
				"本服务器地址；多数软件只需要这一项，其余会通过发现文档自动获取。",
		},
		Examples:   []string{"app list", "app list --json"},
		SeeAlso:    []string{"app show", "app create"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/applications"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/applications", nil)
			if err != nil {
				return err
			}
			apps := appRows(data)
			rows := make([][]string, 0, len(apps))
			for _, app := range apps {
				kind := "confidential"
				if !asBoolVal(app["confidential"]) {
					kind = "public"
				}
				rows = append(rows, []string{
					asStr(app["id"]), asStr(app["name"]), asStr(app["client_id"]), kind,
					yesNo(asBoolVal(app["trusted"])), yesNo(asBoolVal(app["disabled"])),
					joinStrings(app["scopes"], " "),
				})
			}
			if err := rt.Table([]string{"id", "name", "client_id", "kind", "trusted", "disabled", "scopes"}, rows); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			issuer := asStr(asMap(data)["issuer"])
			if rt.Session.Lang == "zh" {
				rt.Printf("\nissuer：%s\n", issuer)
			} else {
				rt.Printf("\nissuer: %s\n", issuer)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "app show",
		Group:   "instance",
		Summary: Text{EN: "Show one sign-in application", ZH: "显示单个登录应用"},
		Usage:   "app show <id>",
		Help: Text{
			EN: "Prints the callbacks exactly as they are registered. They are matched by string " +
				"equality, so a trailing slash is a different callback — which is what a failing " +
				"integration usually turns out to be.",
			ZH: "会原样打印已登记的回调地址。回调是按字符串完全匹配的，多一个结尾斜杠就是另一个地址——" +
				"对接失败时通常就是这个原因。",
		},
		Args:       []Arg{{Name: "id", Hint: Text{EN: "application id, from app list", ZH: "应用 id，来自 app list"}, Required: true}},
		Examples:   []string{"app show 01H9Z…", "app show 01H9Z… --json"},
		SeeAlso:    []string{"app list", "app edit"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/applications"},
		Run: func(_ context.Context, rt *Runtime) error {
			wanted, err := requireRef(rt, "application id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/applications", nil)
			if err != nil {
				return err
			}
			for _, app := range appRows(data) {
				if asStr(app["id"]) != wanted && asStr(app["client_id"]) != wanted {
					continue
				}
				kind := "confidential"
				if !asBoolVal(app["confidential"]) {
					kind = "public (PKCE)"
				}
				return rt.Fields([][2]string{
					{"id", asStr(app["id"])},
					{"name", asStr(app["name"])},
					{"description", asStr(app["description"])},
					{"client_id", asStr(app["client_id"])},
					{"kind", kind},
					{"redirect_uris", joinStrings(app["redirect_uris"], "\n                ")},
					{"scopes", joinStrings(app["scopes"], " ")},
					{"trusted", yesNo(asBoolVal(app["trusted"]))},
					{"disabled", yesNo(asBoolVal(app["disabled"]))},
					{"created", formatMS(app["created_at"])},
				})
			}
			if rt.Session.Lang == "zh" {
				return rt.Errorf("没有这个应用。")
			}
			return rt.Errorf("no such application.")
		},
	})

	registerCommand(Command{
		Name:    "app create",
		Group:   "instance",
		Summary: Text{EN: "Register an application that may sign people in", ZH: "登记一个可以用本站账号登录的应用"},
		Usage:   "app create --name TEXT --redirect URLS [--scopes LIST] [--public] [--trusted]",
		Help: Text{
			EN: "--redirect takes one or more callback URLs, comma-separated, matched later by exact " +
				"string equality. https is required except on localhost. --public is for software " +
				"with nowhere to keep a secret, such as a single-page app: it gets no client secret " +
				"and must use PKCE. --trusted skips the consent screen, which is for the operator's " +
				"own services and nothing else, and only a super administrator may set it. The " +
				"client secret is printed once, here.",
			ZH: "--redirect 接受一个或多个回调地址，用逗号分隔，之后按字符串完全匹配。除 localhost 外必须是 " +
				"https。--public 用于没有地方存放密钥的软件（例如纯前端应用）：它不会获得 client secret，" +
				"必须使用 PKCE。--trusted 会跳过授权确认页，只适用于运营方自己的服务，且只有超级管理员能设置。" +
				"client secret 只在这里打印一次。",
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-60 characters, shown on the consent screen", ZH: "1-60 字符，会显示在授权页上"}, Value: "TEXT"},
			{Name: "--description", Hint: Text{EN: "one line, shown under the name", ZH: "一行说明，显示在名称下方"}, Value: "TEXT"},
			{Name: "--redirect", Hint: Text{EN: "comma-separated callback URLs", ZH: "逗号分隔的回调地址"}, Value: "URLS"},
			{Name: "--scopes", Hint: Text{EN: "space or comma separated; default openid profile email", ZH: "空格或逗号分隔；默认 openid profile email"}, Value: "LIST"},
			{Name: "--public", Hint: Text{EN: "no client secret; PKCE required", ZH: "不签发 client secret；必须使用 PKCE"}},
			{Name: "--trusted", Hint: Text{EN: "skip the consent screen", ZH: "跳过授权确认页"}},
		},
		Examples: []string{
			"app create --name Wiki --redirect https://wiki.example.com/oidc/callback",
			"app create --name Desktop --public --redirect http://localhost:8080/callback",
		},
		SeeAlso:    []string{"app list", "app secret", "app delete"},
		Permission: "security",
		Endpoints:  []string{"POST /api/admin/applications"},
		Run: func(_ context.Context, rt *Runtime) error {
			name := rt.String("name")
			redirect := rt.String("redirect")
			if name == "" || redirect == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --name 和 --redirect")
				}
				return rt.Errorf("--name and --redirect are required")
			}

			body := map[string]any{
				"name":          name,
				"redirect_uris": strings.ReplaceAll(redirect, ",", "\n"),
				"public":        rt.Bool("public"),
				"trusted":       rt.Bool("trusted"),
			}
			if description := rt.String("description"); description != "" {
				body["description"] = description
			}
			if scopes := rt.String("scopes"); scopes != "" {
				body["scopes"] = strings.Fields(strings.ReplaceAll(scopes, ",", " "))
			}

			data, _, err := rt.Call(http.MethodPost, "/api/admin/applications", body)
			if err != nil {
				return err
			}
			result := asMap(data)
			app := asMap(result["application"])
			fields := [][2]string{
				{"id", asStr(app["id"])},
				{"name", asStr(app["name"])},
				{"client_id", asStr(app["client_id"])},
			}
			if secret := asStr(result["client_secret"]); secret != "" {
				fields = append(fields, [2]string{"client_secret", secret})
			}
			if err := rt.Fields(fields); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("\nclient secret 只显示这一次，请立即保存。\n")
			} else {
				rt.Printf("\nthe client secret is shown once; save it now.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "app edit",
		Group:   "instance",
		Summary: Text{EN: "Change a sign-in application", ZH: "修改登录应用"},
		Usage:   "app edit <id> [--name TEXT] [--redirect URLS] [--scopes LIST] [--trusted BOOL] [--disabled BOOL]",
		Help: Text{
			EN: "Only the flags you give are changed. --disabled true stops the application signing " +
				"anybody in and makes the tokens it already holds stop answering, which is the switch " +
				"to reach for when its secret has leaked; --scopes narrows what it may ask for, and a " +
				"narrower list takes effect on the next sign-in rather than on tokens already issued. " +
				"Changing the callbacks, or anything at all on a trusted application, needs a super " +
				"administrator: consent is kept per application, so a new callback would receive the " +
				"codes of people who agreed to the old one.",
			ZH: "只会修改你给出的选项。--disabled true 会让该应用无法再登录，且它已持有的令牌立即失效——" +
				"密钥泄露时应当先用这个开关；--scopes 用于收窄它能申请的信息，收窄后在下次登录时生效，" +
				"不影响已签发的令牌。修改回调地址，或修改受信任应用的任何内容，需要超级管理员：" +
				"授权是按应用记录的，新增的回调会收到此前同意过的人的授权码。",
		},
		Args: []Arg{{Name: "id", Hint: Text{EN: "application id", ZH: "应用 id"}, Required: true}},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-60 characters", ZH: "1-60 字符"}, Value: "TEXT"},
			{Name: "--description", Hint: Text{EN: "one line", ZH: "一行说明"}, Value: "TEXT"},
			{Name: "--redirect", Hint: Text{EN: "comma-separated callback URLs, replacing the current list", ZH: "逗号分隔的回调地址，会整体替换现有列表"}, Value: "URLS"},
			{Name: "--scopes", Hint: Text{EN: "space or comma separated", ZH: "空格或逗号分隔"}, Value: "LIST"},
			{Name: "--trusted", Hint: Text{EN: "true or false", ZH: "true 或 false"}, Value: "BOOL"},
			{Name: "--disabled", Hint: Text{EN: "true or false", ZH: "true 或 false"}, Value: "BOOL"},
		},
		Examples:   []string{"app edit 01H9Z… --disabled true", "app edit 01H9Z… --redirect https://wiki.example.com/oidc/callback"},
		SeeAlso:    []string{"app list", "app secret"},
		Permission: "security",
		Endpoints:  []string{"PATCH /api/admin/applications/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			appID, err := requireRef(rt, "application id")
			if err != nil {
				return err
			}
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			body.str(rt, "description", "description")
			body.boolv(rt, "trusted", "trusted")
			body.boolv(rt, "disabled", "disabled")
			if redirect := rt.String("redirect"); redirect != "" {
				body["redirect_uris"] = strings.ReplaceAll(redirect, ",", "\n")
			}
			if scopes := rt.String("scopes"); scopes != "" {
				body["scopes"] = strings.Fields(strings.ReplaceAll(scopes, ",", " "))
			}
			if len(body) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有要修改的内容。")
				}
				return rt.Errorf("nothing to change.")
			}

			data, _, err := rt.Call(http.MethodPatch,
				"/api/admin/applications/"+url.PathEscape(appID), map[string]any(body))
			if err != nil {
				return err
			}
			app := asMap(asMap(data)["application"])
			return rt.Fields([][2]string{
				{"id", asStr(app["id"])},
				{"name", asStr(app["name"])},
				{"redirect_uris", joinStrings(app["redirect_uris"], "\n                ")},
				{"scopes", joinStrings(app["scopes"], " ")},
				{"trusted", yesNo(asBoolVal(app["trusted"]))},
				{"disabled", yesNo(asBoolVal(app["disabled"]))},
			})
		},
	})

	registerCommand(Command{
		Name:    "app secret",
		Group:   "instance",
		Summary: Text{EN: "Issue a new client secret for an application", ZH: "为应用重新签发 client secret"},
		Usage:   "app secret <id> --yes",
		Help: Text{
			EN: "The old secret stops working immediately, so the application cannot sign anybody new " +
				"in until its configuration is updated. Tokens it already holds keep working — they " +
				"were authenticated when they were issued — so disable the application instead if " +
				"what you need is for those to stop.",
			ZH: "旧密钥会立即失效，在对方更新配置之前，该应用无法再登录任何人。它已持有的令牌仍然有效——" +
				"这些令牌在签发时已经完成认证——如果你需要让它们也失效，请改用停用该应用。",
		},
		Args:        []Arg{{Name: "id", Hint: Text{EN: "application id", ZH: "应用 id"}, Required: true}},
		Examples:    []string{"app secret 01H9Z… --yes", "app secret 01H9Z… -y"},
		SeeAlso:     []string{"app edit", "app list"},
		Permission:  "security",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/applications/{id}/secret"},
		Run: func(_ context.Context, rt *Runtime) error {
			appID, err := requireRef(rt, "application id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost,
				"/api/admin/applications/"+url.PathEscape(appID)+"/secret", nil)
			if err != nil {
				return err
			}
			if err := rt.Fields([][2]string{
				{"client_secret", asStr(asMap(data)["client_secret"])},
			}); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("\n只显示这一次，请立即保存。\n")
			} else {
				rt.Printf("\nshown once; save it now.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "app delete",
		Group:   "instance",
		Summary: Text{EN: "Remove a sign-in application", ZH: "删除登录应用"},
		Usage:   "app delete <id> --yes",
		Help: Text{
			EN: "Everything goes with it: the consent every account gave it, and every token it holds. " +
				"Registering it again produces a different client id, so the other side has to be " +
				"reconfigured — disable it instead if this might be temporary.",
			ZH: "与之相关的一切都会一并删除：每个账户对它的授权，以及它持有的全部令牌。" +
				"重新登记会得到不同的 client id，对方必须重新配置——如果只是临时停用，请改用停用。",
		},
		Args:        []Arg{{Name: "id", Hint: Text{EN: "application id", ZH: "应用 id"}, Required: true}},
		Examples:    []string{"app delete 01H9Z… --yes", "app delete 01H9Z… -y"},
		SeeAlso:     []string{"app edit", "app list"},
		Permission:  "security",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/applications/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			appID, err := requireRef(rt, "application id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete,
				"/api/admin/applications/"+url.PathEscape(appID), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已删除。\n")
			} else {
				rt.Printf("deleted.\n")
			}
			return nil
		},
	})
}
