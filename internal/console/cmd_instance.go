package console

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

func init() {
	registerCommand(Command{
		Name:    "setting list",
		Group:   "instance",
		Summary: Text{EN: "List instance settings", ZH: "列出实例设置"},
		Usage:   "setting list [--q TEXT]",
		Help: Text{
			EN: "Only the keys your grants can read are returned at all — a key you cannot read is " +
				"absent, not blanked. turnstile.secret_key is masked by the server when set.",
			ZH: "只会返回你的权限能读取的键——不能读取的键会直接缺失，而不是留空。" +
				"turnstile.secret_key 在已设置时会由服务器打码。",
		},
		Flags:      []Flag{{Name: "--q", Hint: Text{EN: "substring filter on the key name", ZH: "对键名做子串筛选"}, Value: "TEXT"}},
		Examples:   []string{"setting list", "setting list --q attachments"},
		SeeAlso:    []string{"setting get", "setting set"},
		Permission: "settings,security,availability,invites,leaderboard",
		Endpoints:  []string{"GET /api/admin/settings"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/settings", nil)
			if err != nil {
				return err
			}
			settings := asMap(asMap(data)["settings"])
			q := strings.ToLower(rt.String("q"))
			keys := make([]string, 0, len(settings))
			for k := range settings {
				if q == "" || strings.Contains(strings.ToLower(k), q) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				rows = append(rows, []string{k, asStr(settings[k])})
			}
			return rt.Table([]string{"key", "value"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "setting get",
		Group:      "instance",
		Summary:    Text{EN: "Show one setting", ZH: "显示单个设置"},
		Usage:      "setting get <key>",
		Args:       []Arg{{Name: "key", Hint: Text{EN: "the setting key", ZH: "设置键"}, Required: true}},
		Examples:   []string{"setting get site.name", "setting get attachments.max_mb"},
		SeeAlso:    []string{"setting list", "setting set"},
		Permission: "settings,security,availability,invites,leaderboard",
		Endpoints:  []string{"GET /api/admin/settings"},
		Run: func(_ context.Context, rt *Runtime) error {
			key, err := requireRef(rt, "setting key")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/settings", nil)
			if err != nil {
				return err
			}
			settings := asMap(asMap(data)["settings"])
			v, ok := settings[key]
			if !ok {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有这个键，或你无权读取它。")
				}
				return rt.Errorf("no such key, or you do not have permission to read it.")
			}
			return rt.Fields([][2]string{{key, asStr(v)}})
		},
	})

	registerCommand(Command{
		Name:    "setting set",
		Group:   "instance",
		Summary: Text{EN: "Set one instance setting", ZH: "设置单个实例设置"},
		Usage:   "setting set <key> <value>",
		Help: Text{
			EN: "Every value is sent as a string, including booleans ('true') and numbers — the server " +
				"decides the type per key. Which grant this needs depends on the key's prefix: health.* " +
				"needs availability, registration.*/turnstile.*/security.*/oauth.* need security " +
				"(registration.enabled also takes invites), invites.* needs invites, " +
				"everything else needs settings.",
			ZH: "所有值都以字符串形式发送，包括布尔值（'true'）和数字——具体类型由服务器按键名判断。" +
				"所需权限取决于键名前缀：health.* 需要 availability，" +
				"registration.*/turnstile.*/security.*/oauth.* 需要 security（registration.enabled 也可以用 invites），" +
				"invites.* 需要 invites，其余需要 settings。",
		},
		Args: []Arg{
			{Name: "key", Hint: Text{EN: "the setting key", ZH: "设置键"}, Required: true},
			{Name: "value", Hint: Text{EN: "the new value, as a string", ZH: "新值，以字符串形式给出"}, Required: true},
		},
		Examples:   []string{"setting set site.name 'Obsidian Arc'", "setting set attachments.max_mb 12"},
		SeeAlso:    []string{"setting list", "setting import"},
		Permission: "settings,security,availability,invites,leaderboard",
		Endpoints:  []string{"PUT /api/admin/settings"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() < 2 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要键和值")
				}
				return rt.Errorf("a key and a value are required")
			}
			key := rt.Arg(0)
			value := strings.Join(rt.Args()[1:], " ")
			data, _, err := rt.Call(http.MethodPut, "/api/admin/settings", map[string]any{key: value})
			if err != nil {
				return err
			}
			settings := asMap(asMap(data)["settings"])
			return rt.Fields([][2]string{{key, asStr(settings[key])}})
		},
	})

	registerCommand(Command{
		Name:    "setting import",
		Group:   "instance",
		Summary: Text{EN: "Bulk-import settings from a JSON document", ZH: "从 JSON 文档批量导入设置"},
		Usage:   "setting import <json> --yes",
		Help: Text{
			EN: "The argument is a flat {\"key\":\"value\"} document — typically a previous `setting " +
				"list --json`'s settings object, pasted back. Unlike setting set, a bad entry here is " +
				"dropped and named in the response rather than failing the whole import. Unlike setting " +
				"set, this does NOT protect turnstile.secret_key from being overwritten by a masked " +
				"'••••••••' placeholder if that is literally what the document contains.",
			ZH: "参数是一份扁平的 {\"key\":\"value\"} 文档——通常是之前 `setting list --json` 返回的 " +
				"settings 对象，直接粘贴回来。与 setting set 不同，这里某一条有问题只会被跳过并在返回结果中" +
				"列出，不会导致整个导入失败。与 setting set 不同，若文档中 turnstile.secret_key 的值恰好是" +
				"打码占位符 '••••••••'，这里不会阻止它被原样写入覆盖已保存的密钥。",
		},
		Args:        []Arg{{Name: "json", Hint: Text{EN: "the import document", ZH: "导入文档"}, Required: true}},
		Examples:    []string{`setting import '{"site.name":"Obsidian Arc"}' --yes`, "setting import '{...}' -y"},
		Permission:  "settings",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/settings/import"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要一份 JSON 文档")
				}
				return rt.Errorf("a JSON document is required")
			}
			body, err := parseJSONBody(rt, strings.Join(rt.Args(), " "))
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/settings/import", body)
			if err != nil {
				return err
			}
			m := asMap(data)
			var skipped []string
			for _, s := range asSlice(m["skipped"]) {
				skipped = append(skipped, asStr(s))
			}
			return rt.Fields([][2]string{{"applied", fmt.Sprint(asNum(m["applied"]))}, {"skipped", strings.Join(skipped, "; ")}})
		},
	})

	registerCommand(Command{
		Name:    "attachment purge",
		Group:   "instance",
		Summary: Text{EN: "Discard the bytes of every already-sent attachment", ZH: "丢弃全部已发送附件的数据"},
		Usage:   "attachment purge --yes",
		Help: Text{
			EN: "Immediate and irreversible: every attachment already attached to a message loses its " +
				"stored bytes. Uploads not yet sent are untouched (governed instead by " +
				"attachments.orphan_minutes).",
			ZH: "立即执行且不可撤销：已附加到消息的全部附件都会丢失存储的数据。" +
				"尚未发送的上传不受影响（由 attachments.orphan_minutes 单独管理）。",
		},
		Examples:    []string{"attachment purge --yes", "attachment purge -y"},
		Permission:  "settings",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/attachments/purge"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodPost, "/api/admin/attachments/purge", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			att := asMap(m["attachments"])
			return rt.Fields([][2]string{
				{"purged", fmt.Sprint(asNum(m["purged"]))}, {"held", fmt.Sprint(asNum(att["held"]))}, {"bytes", fmt.Sprint(asNum(att["bytes"]))},
			})
		},
	})

	registerCommand(Command{
		Name:       "notice list",
		Group:      "instance",
		Summary:    Text{EN: "List announcements", ZH: "列出公告"},
		Usage:      "notice list",
		Examples:   []string{"notice list", "notice list --json"},
		SeeAlso:    []string{"notice create", "notice edit"},
		Permission: "announcements",
		Endpoints:  []string{"GET /api/admin/announcements"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/announcements", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["announcements"]) {
				a := asMap(raw)
				rows = append(rows, []string{
					asStr(a["id"]), asStr(a["title"]), asStr(a["display_mode"]),
					yesNo(asBoolVal(a["published"])), yesNo(asBoolVal(a["pinned"])), formatMS(a["created_at"]),
				})
			}
			return rt.Table([]string{"id", "title", "display_mode", "published", "pinned", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "notice create",
		Group:   "instance",
		Summary: Text{EN: "Create an announcement", ZH: "创建公告"},
		Usage:   "notice create --title TEXT [--body TEXT] [--mode always|once|silent] [--dismiss-after N] [--published] [--pinned]",
		Flags: []Flag{
			{Name: "--title", Hint: Text{EN: "1-120 characters, required", ZH: "1-120 字符，必填"}, Value: "TEXT"},
			{Name: "--body", Hint: Text{EN: "≤32768 bytes", ZH: "≤32768 字节"}, Value: "TEXT"},
			{Name: "--mode", Hint: Text{EN: "always, once or silent; empty = once", ZH: "always、once 或 silent；留空则为 once"}, Value: "MODE"},
			{Name: "--dismiss-after", Hint: Text{EN: "seconds before it can be dismissed", ZH: "多少秒后可关闭"}, Value: "N"},
			{Name: "--published", Hint: Text{EN: "make it visible now", ZH: "立即公开可见"}},
			{Name: "--pinned", Hint: Text{EN: "pin it above other announcements", ZH: "置顶于其他公告之上"}},
		},
		Examples:   []string{"notice create --title 'Maintenance tonight' --published", "notice create --title Welcome --mode always --pinned"},
		SeeAlso:    []string{"notice list"},
		Permission: "announcements",
		Endpoints:  []string{"POST /api/admin/announcements"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !rt.Present("title") || rt.String("title") == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --title")
				}
				return rt.Errorf("--title is required")
			}
			body := bodyBuilder{"title": rt.String("title")}
			body.str(rt, "body", "body")
			body.str(rt, "mode", "display_mode")
			body.intv(rt, "dismiss-after", "dismiss_after_seconds")
			body.boolv(rt, "published", "published")
			body.boolv(rt, "pinned", "pinned")
			data, _, err := rt.Call(http.MethodPost, "/api/admin/announcements", map[string]any(body))
			if err != nil {
				return err
			}
			a := asMap(asMap(data)["announcement"])
			return rt.Fields([][2]string{{"id", asStr(a["id"])}, {"title", asStr(a["title"])}, {"created_at", formatMS(a["created_at"])}})
		},
	})

	registerCommand(Command{
		Name:    "notice edit",
		Group:   "instance",
		Summary: Text{EN: "Edit an announcement", ZH: "编辑公告"},
		Usage:   "notice edit <id> [--title TEXT] [--body TEXT] [--mode MODE] [--dismiss-after N] [--published BOOL] [--pinned BOOL]",
		Help: Text{
			EN: "This is a full replace despite being PATCH on the wire: the console reads the " +
				"announcement first and resends every field, so a flag you leave out keeps its current " +
				"value rather than being cleared — unlike most other edit commands in this console.",
			ZH: "尽管线上是 PATCH，这里实际是整体替换：控制台会先读取该公告并重新发送全部字段，" +
				"因此未给出的选项会保留原值而不会被清空——这一点与本控制台中大多数其他编辑命令不同。",
		},
		Args: []Arg{{Name: "id", Hint: Text{EN: "announcement id, from notice list", ZH: "公告 id，来自 notice list"}, Required: true}},
		Flags: []Flag{
			{Name: "--title", Hint: Text{EN: "1-120 characters", ZH: "1-120 字符"}, Value: "TEXT"},
			{Name: "--body", Hint: Text{EN: "≤32768 bytes", ZH: "≤32768 字节"}, Value: "TEXT"},
			{Name: "--mode", Hint: Text{EN: "always, once or silent", ZH: "always、once 或 silent"}, Value: "MODE"},
			{Name: "--dismiss-after", Hint: Text{EN: "seconds before it can be dismissed", ZH: "多少秒后可关闭"}, Value: "N"},
			{Name: "--published", Hint: Text{EN: "true or false", ZH: "true 或 false"}, Value: "BOOL"},
			{Name: "--pinned", Hint: Text{EN: "true or false", ZH: "true 或 false"}, Value: "BOOL"},
		},
		Examples:   []string{"notice edit 01H8X… --published true", "notice edit 01H8X… --title 'Updated notice'"},
		SeeAlso:    []string{"notice list"},
		Permission: "announcements",
		Endpoints:  []string{"GET /api/admin/announcements", "PATCH /api/admin/announcements/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "announcement id")
			if err != nil {
				return err
			}
			existingData, _, err := rt.Call(http.MethodGet, "/api/admin/announcements", nil)
			if err != nil {
				return err
			}
			var cur map[string]any
			for _, raw := range asSlice(asMap(existingData)["announcements"]) {
				if a := asMap(raw); asStr(a["id"]) == ref {
					cur = a
					break
				}
			}
			if cur == nil {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有这个公告。")
				}
				return rt.Errorf("no such announcement.")
			}
			body := map[string]any{
				"title": asStr(cur["title"]), "body": asStr(cur["body"]), "display_mode": asStr(cur["display_mode"]),
				"dismiss_after_seconds": int(asNum(cur["dismiss_after_seconds"])),
				"published":             asBoolVal(cur["published"]), "pinned": asBoolVal(cur["pinned"]),
			}
			if rt.Present("title") {
				body["title"] = rt.String("title")
			}
			if rt.Present("body") {
				body["body"] = rt.String("body")
			}
			if rt.Present("mode") {
				body["display_mode"] = rt.String("mode")
			}
			if rt.Present("dismiss-after") {
				body["dismiss_after_seconds"] = rt.Int("dismiss-after")
			}
			if rt.Present("published") {
				body["published"] = rt.Bool("published")
			}
			if rt.Present("pinned") {
				body["pinned"] = rt.Bool("pinned")
			}
			data, _, err := rt.Call(http.MethodPatch, "/api/admin/announcements/"+url.PathEscape(ref), body)
			if err != nil {
				return err
			}
			a := asMap(asMap(data)["announcement"])
			return rt.Fields([][2]string{{"id", asStr(a["id"])}, {"title", asStr(a["title"])}, {"updated_at", formatMS(a["updated_at"])}})
		},
	})

	registerCommand(Command{
		Name:        "notice delete",
		Group:       "instance",
		Summary:     Text{EN: "Delete an announcement", ZH: "删除公告"},
		Usage:       "notice delete <id> --yes",
		Args:        []Arg{{Name: "id", Hint: Text{EN: "announcement id, from notice list", ZH: "公告 id，来自 notice list"}, Required: true}},
		Examples:    []string{"notice delete 01H8X… --yes", "notice delete 01H8X… -y"},
		SeeAlso:     []string{"notice list"},
		Permission:  "announcements",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/announcements/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "announcement id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/announcements/"+url.PathEscape(ref), nil); err != nil {
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

	registerCommand(Command{
		Name:    "security events",
		Group:   "instance",
		Summary: Text{EN: "List security events", ZH: "列出安全事件"},
		Usage:   "security events [--event TYPE] [--severity info|warning|danger] [--decision TEXT] [--user REF] [--since MS] [--limit N] [--offset N]",
		Flags: []Flag{
			{Name: "--event", Hint: Text{EN: "signup_review, api_restriction, api_restriction_lifted or chat_challenge", ZH: "signup_review、api_restriction、api_restriction_lifted 或 chat_challenge"}, Value: "TYPE"},
			{Name: "--severity", Hint: Text{EN: "info, warning or danger", ZH: "info、warning 或 danger"}, Value: "SEVERITY"},
			{Name: "--decision", Hint: Text{EN: "exact match, e.g. allow or restrict", ZH: "精确匹配，例如 allow 或 restrict"}, Value: "TEXT"},
			{Name: "--user", Hint: Text{EN: "filter to one account, by ref", ZH: "按账户筛选，可用引用"}, Value: "REF"},
			{Name: "--since", Hint: Text{EN: "epoch ms", ZH: "毫秒时间戳"}, Value: "MS"},
			{Name: "--limit", Hint: Text{EN: "page size, 1-200, default 50", ZH: "每页数量，1-200，默认 50"}, Value: "N", Default: "50"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
		},
		Examples:   []string{"security events --severity danger", "security events --event api_restriction --user alice"},
		SeeAlso:    []string{"security review"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/security/events"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			for _, kv := range [][2]string{{"event", "event"}, {"severity", "severity"}, {"decision", "decision"}} {
				if v := rt.String(kv[0]); v != "" {
					q.Set(kv[1], v)
				}
			}
			if v := rt.String("user"); v != "" {
				uid, err := resolveUserRef(rt, v)
				if err != nil {
					return err
				}
				q.Set("user_id", uid)
			}
			if v := rt.String("since"); v != "" {
				q.Set("since", v)
			}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/security/events?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["events"]) {
				e := asMap(raw)
				rows = append(rows, []string{
					formatMS(e["at"]), asStr(e["event"]), asStr(e["severity"]), asStr(e["username"]),
					asStr(e["decision"]), asStr(e["reason"]), asStr(e["ip"]),
				})
			}
			return rt.Table([]string{"at", "event", "severity", "user", "decision", "reason", "ip"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "security review",
		Group:   "instance",
		Summary: Text{EN: "Trial-run the sign-up reviewer against a hypothetical account", ZH: "对一个假设账户试运行注册审核"},
		Usage:   "security review --username TEXT [--email TEXT] [--qq TEXT] [--nickname TEXT] [--user-agent TEXT] [--from-this-address N]",
		Help: Text{
			EN: "Nothing is created; this only asks the configured reviewer what it would decide. " +
				"400s if this build has no reviewer configured.",
			ZH: "不会创建任何账户；只是询问已配置的审核器会如何判定。若本构建未配置审核器则返回 400。",
		},
		Flags: []Flag{
			{Name: "--username", Hint: Text{EN: "required", ZH: "必填"}, Value: "TEXT"},
			{Name: "--email", Hint: Text{EN: "", ZH: ""}, Value: "TEXT"},
			{Name: "--qq", Hint: Text{EN: "", ZH: ""}, Value: "TEXT"},
			{Name: "--nickname", Hint: Text{EN: "", ZH: ""}, Value: "TEXT"},
			{Name: "--user-agent", Hint: Text{EN: "what the client would have claimed", ZH: "客户端本会声称的 UA"}, Value: "TEXT"},
			{Name: "--from-this-address", Hint: Text{EN: "how many prior sign-ups from that IP to simulate", ZH: "模拟该 IP 之前的注册次数"}, Value: "N"},
		},
		Examples:   []string{"security review --username testuser123", "security review --username testuser123 --qq 12345678 --from-this-address 3"},
		SeeAlso:    []string{"security events"},
		Permission: "security",
		Endpoints:  []string{"POST /api/admin/security/review"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !rt.Present("username") || rt.String("username") == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --username")
				}
				return rt.Errorf("--username is required")
			}
			body := bodyBuilder{"username": rt.String("username")}
			body.str(rt, "email", "email")
			body.str(rt, "qq", "qq")
			body.str(rt, "nickname", "nickname")
			body.str(rt, "user-agent", "user_agent")
			body.intv(rt, "from-this-address", "from_this_address")
			data, _, err := rt.Call(http.MethodPost, "/api/admin/security/review", map[string]any(body))
			if err != nil {
				return err
			}
			m := asMap(data)
			return rt.Fields([][2]string{{"ran", yesNo(asBoolVal(m["ran"]))}, {"decision", asStr(m["decision"])}, {"reason", asStr(m["reason"])}})
		},
	})

	registerCommand(Command{
		Name:       "meta",
		Group:      "instance",
		Summary:    Text{EN: "Show the provider kinds and reasoning styles this build supports", ZH: "显示此构建支持的服务商类型与推理风格"},
		Usage:      "meta",
		Examples:   []string{"meta", "meta --json"},
		Permission: "",
		Endpoints:  []string{"GET /api/admin/meta"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/meta", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			var kinds, styles []string
			for _, v := range asSlice(m["provider_kinds"]) {
				kinds = append(kinds, asStr(v))
			}
			for _, v := range asSlice(m["reasoning_styles"]) {
				styles = append(styles, asStr(v))
			}
			return rt.Fields([][2]string{{"provider_kinds", strings.Join(kinds, ", ")}, {"reasoning_styles", strings.Join(styles, ", ")}})
		},
	})

	registerCommand(Command{
		Name:    "refs",
		Group:   "instance",
		Summary: Text{EN: "Show the selector lists (groups/models/providers) visible to you", ZH: "显示你可见的选择器列表（分组/模型/服务商）"},
		Usage:   "refs",
		Help: Text{
			EN: "Each list is present only when its own gate is met, which is not the same grant as the " +
				"list's own domain — see help groups / help model list for the domain commands proper. " +
				"This is what the by-name resolution every other command uses reads from.",
			ZH: "每份列表只有在满足各自的权限门槛时才会出现，且该门槛未必等于该领域本身的权限——" +
				"完整的领域命令见 help groups / help model list。本控制台其它命令的按名称解析" +
				"就是读取这里的数据。",
		},
		Examples:   []string{"refs", "refs --json"},
		Permission: "",
		Endpoints:  []string{"GET /api/admin/references"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/references", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			if rt.effectiveJSON() {
				// rt.Fields(nil) still does the right thing here: with JSON
				// mode in effect it ignores the (empty) pairs and prints the
				// verbatim response body Call just captured — exactly what
				// --json is for, without this command needing its own copy
				// of that branch.
				return rt.Fields(nil)
			}
			for _, section := range []string{"groups", "models", "providers"} {
				items, ok := m[section]
				if !ok {
					continue
				}
				fmt.Fprintf(rt.Out, "%s:\n", section)
				var rows [][]string
				for _, raw := range asSlice(items) {
					it := asMap(raw)
					label := asStr(it["name"])
					if label == "" {
						label = asStr(it["display_name"])
					}
					rows = append(rows, []string{asStr(it["id"]), label})
				}
				RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"id", "name"}, rows)
				fmt.Fprintln(rt.Out)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "member-options",
		Group:   "groups",
		Summary: Text{EN: "Search accounts for group assignment", ZH: "搜索账户以用于分组分配"},
		Usage:   "member-options [--q TEXT] [--group REF] [--limit N] [--offset N]",
		Help: Text{
			EN: "This is what `group assign`/`group members` use to resolve a username to an id without " +
				"needing the 'users' grant. --group narrows to (or excludes, depending on the UI's own " +
				"use of this endpoint) one group's current membership.",
			ZH: "这是 `group assign`/`group members` 用来在不需要 'users' 权限的情况下将用户名解析为 id " +
				"所依赖的接口。--group 用于按某个分组的当前成员范围筛选。",
		},
		Flags: []Flag{
			{Name: "--q", Hint: Text{EN: "search text", ZH: "搜索关键字"}, Value: "TEXT"},
			{Name: "--group", Hint: Text{EN: "group ref", ZH: "分组引用"}, Value: "REF"},
			{Name: "--limit", Hint: Text{EN: "page size, default 20", ZH: "每页数量，默认 20"}, Value: "N", Default: "20"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
		},
		Examples:   []string{"member-options --q alice", "member-options --group Trial"},
		Permission: "groups",
		Endpoints:  []string{"GET /api/admin/member-options"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			if v := rt.String("q"); v != "" {
				q.Set("q", v)
			}
			if v := rt.String("group"); v != "" {
				gid, err := resolveGroupRef(rt, v)
				if err != nil {
					return err
				}
				q.Set("group_id", gid)
			}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 20)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/member-options?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["users"]) {
				u := asMap(raw)
				rows = append(rows, []string{asStr(u["id"]), asStr(u["username"]), asStr(u["nickname"]), asStr(u["group_id"]), formatMS(u["group_expires_at"])})
			}
			return rt.Table([]string{"id", "username", "nickname", "group_id", "expires"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "login-bg set",
		Group:   "instance",
		Summary: Text{EN: "Set a login background image", ZH: "设置登录背景图"},
		Usage:   "login-bg set <variant> <file-or-base64>",
		Help: Text{
			EN: "Upload or set a login background image for landscape_light, landscape_dark, portrait_light, or portrait_dark.",
			ZH: "为 landscape_light、landscape_dark、portrait_light 或 portrait_dark 设置或上传登录背景图。",
		},
		Args: []Arg{
			{Name: "variant", Hint: Text{EN: "landscape_light, landscape_dark, portrait_light, or portrait_dark", ZH: "landscape_light、landscape_dark、portrait_light 或 portrait_dark"}, Required: true},
			{Name: "image", Hint: Text{EN: "image file path or base64 data", ZH: "图片文件路径或 base64 数据"}, Required: true},
		},
		Examples:   []string{"login-bg set landscape_light /path/to/image.png", "login-bg set portrait_dark /path/to/image.jpg"},
		Permission: "settings",
		Endpoints:  []string{"PUT /api/admin/login-background/{variant}"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() < 2 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要变种名称和图片路径或数据")
				}
				return rt.Errorf("variant and image are required")
			}
			variant := rt.Arg(0)
			rawImage := rt.Arg(1)

			var mime, data string
			if fileBytes, err := os.ReadFile(rawImage); err == nil {
				mime = http.DetectContentType(fileBytes)
				data = base64.StdEncoding.EncodeToString(fileBytes)
			} else {
				mime = "image/jpeg"
				data = rawImage
			}

			respData, _, err := rt.Call(http.MethodPut, "/api/admin/login-background/"+variant, map[string]string{
				"mime": mime,
				"data": data,
			})
			if err != nil {
				return err
			}
			urlStr := asStr(asMap(respData)["url"])
			if rt.Session.Lang == "zh" {
				rt.Printf("登录背景图已更新：%s\n", urlStr)
			} else {
				rt.Printf("Login background updated: %s\n", urlStr)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "login-bg clear",
		Group:   "instance",
		Summary: Text{EN: "Clear a login background image", ZH: "清除登录背景图"},
		Usage:   "login-bg clear <variant>",
		Help: Text{
			EN: "Remove a login background image variant (landscape_light, landscape_dark, portrait_light, or portrait_dark).",
			ZH: "清除指定的登录背景图变种（landscape_light、landscape_dark、portrait_light 或 portrait_dark）。",
		},
		Args: []Arg{
			{Name: "variant", Hint: Text{EN: "landscape_light, landscape_dark, portrait_light, or portrait_dark", ZH: "landscape_light、landscape_dark、portrait_light 或 portrait_dark"}, Required: true},
		},
		Examples:    []string{"login-bg clear landscape_light", "login-bg clear portrait_dark"},
		Permission:  "settings",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/login-background/{variant}"},
		Run: func(_ context.Context, rt *Runtime) error {
			variant, err := requireRef(rt, "variant")
			if err != nil {
				return err
			}
			_, _, err = rt.Call(http.MethodDelete, "/api/admin/login-background/"+variant, nil)
			if err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已清除变种 %s 的登录背景图。\n", variant)
			} else {
				rt.Printf("Cleared login background for %s.\n", variant)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "logo set",
		Group:   "instance",
		Summary: Text{EN: "Set the site logo", ZH: "设置站点 Logo"},
		Usage:   "logo set <file-or-base64>",
		Help: Text{
			EN: "Upload or set a custom site logo (used for header brand, browser favicon, and PWA icon).",
			ZH: "设置或上传自定义站点 Logo（用作顶部导航栏图标、浏览器 Favicon 及 PWA 应用图标）。",
		},
		Args: []Arg{
			{Name: "image", Hint: Text{EN: "image file path or base64 data", ZH: "图片文件路径或 base64 数据"}, Required: true},
		},
		Examples:   []string{"logo set /path/to/logo.png", "logo set /path/to/logo.svg"},
		Permission: "settings",
		Endpoints:  []string{"PUT /api/admin/logo"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() < 1 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要图片路径或数据")
				}
				return rt.Errorf("image is required")
			}
			rawImage := rt.Arg(0)

			var mime, data string
			if fileBytes, err := os.ReadFile(rawImage); err == nil {
				mime = http.DetectContentType(fileBytes)
				data = base64.StdEncoding.EncodeToString(fileBytes)
			} else {
				mime = "image/png"
				data = rawImage
			}

			respData, _, err := rt.Call(http.MethodPut, "/api/admin/logo", map[string]string{
				"mime": mime,
				"data": data,
			})
			if err != nil {
				return err
			}
			urlStr := asStr(asMap(respData)["url"])
			if rt.Session.Lang == "zh" {
				rt.Printf("站点 Logo 已更新：%s\n", urlStr)
			} else {
				rt.Printf("Site logo updated: %s\n", urlStr)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "logo clear",
		Group:   "instance",
		Summary: Text{EN: "Clear the site logo", ZH: "清除站点 Logo"},
		Usage:   "logo clear",
		Help: Text{
			EN: "Remove the custom site logo and revert to the built-in mark.",
			ZH: "移除自定义站点 Logo 并恢复内置图标。",
		},
		Permission:  "settings",
		Destructive: true,
		Examples:    []string{"logo clear", "logo clear --yes"},
		Endpoints:   []string{"DELETE /api/admin/logo"},
		Run: func(_ context.Context, rt *Runtime) error {
			_, _, err := rt.Call(http.MethodDelete, "/api/admin/logo", nil)
			if err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已清除站点 Logo，恢复为默认图标。\n")
			} else {
				rt.Printf("Cleared site logo, restored default icon.\n")
			}
			return nil
		},
	})
}
