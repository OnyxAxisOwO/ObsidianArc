package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// ---------------------------------------------------------------------
// Shared helpers, used by every cmd_*.go file in this package. They live
// here rather than in registry.go because they are command-authoring
// convenience built on top of the engine, not engine surface — registry.go
// stays the one file every other agent's Runtime calls compile against.
// ---------------------------------------------------------------------

// asMap/asSlice/asStr/asBoolVal/asNum read a field out of the generic
// encoding/json shapes Runtime.Call decodes into (map[string]any, []any,
// string, bool, float64), returning the zero value instead of panicking
// when a key is absent or the wrong dynamic type — which is normal here: a
// show command reads a nested field that a different server version might
// have renamed or omitted, and a wrong render beats a panic on the console.
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asSlice(v any) []any        { s, _ := v.([]any); return s }
func asStr(v any) string         { s, _ := v.(string); return s }
func asBoolVal(v any) bool       { b, _ := v.(bool); return b }
func asNum(v any) float64        { n, _ := v.(float64); return n }
func asInt64Val(v any) int64     { return int64(asNum(v)) }

// formatTime renders an epoch-millisecond timestamp the way every list and
// show command in this package does: UTC, minute precision, "-" for zero —
// every *_at field in the admin API uses 0 to mean "never" or "permanent".
func formatTime(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
}

func formatMS(v any) string { return formatTime(asInt64Val(v)) }

// limitStr renders a nullable quota limit (quota.Summary's limit_requests /
// limit_tokens / limit_credits): null means "not set at any level", which
// is a real, distinct answer from zero and must not print as "0".
func limitStr(v any) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(asNum(v))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// splitCSV turns "a, b ,c" into ["a","b","c"], dropping empty entries —
// used by every flag that accepts a comma list (--permissions, --models,
// --grants).
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func formatWindows(raw any) string {
	slice := asSlice(raw)
	if len(slice) == 0 {
		return "full"
	}
	parts := make([]string, 0, len(slice))
	for _, item := range slice {
		if s := asStr(item); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "full"
	}
	return strings.Join(parts, ",")
}

// bodyBuilder collects PATCH/POST fields, setting a key only when its flag
// was actually given on the line. Every write route in API.md treats an
// absent field as "leave alone" (a pointer on the server side) — guessing a
// zero value here would turn "I didn't mention nickname" into "clear the
// nickname", which is the opposite of every edit command's contract.
type bodyBuilder map[string]any

func (b bodyBuilder) str(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		b[key] = rt.String(flag)
	}
}

func (b bodyBuilder) intv(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		b[key] = rt.Int(flag)
	}
}

func (b bodyBuilder) int64v(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		n, _ := strconv.ParseInt(rt.String(flag), 10, 64)
		b[key] = n
	}
}

func (b bodyBuilder) floatv(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		f, _ := strconv.ParseFloat(rt.String(flag), 64)
		b[key] = f
	}
}

func (b bodyBuilder) boolv(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		b[key] = rt.Bool(flag)
	}
}

// resolveByFields is what every "accept a name where the UI would accept an
// id" convenience in this package boils down to, once the right list has
// been fetched: match one or more fields case-insensitively, exact matches
// before substring matches, and refuse to guess between two matches at the
// same tier — an admin fat-fingering "user delete alice" must never delete
// "alice2" because it happened to be the only substring match left after
// "alice" the exact match was also found (exact always wins outright, so
// this only turns ambiguous when two rows tie at the same tier).
func resolveByFields(lang, kindEN, kindZH, ref string, items []any, labelField string, fields ...string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(ref))
	var exact, partial []map[string]any
	for _, raw := range items {
		m := asMap(raw)
		isExact, isPartial := false, false
		for _, f := range fields {
			v := strings.ToLower(asStr(m[f]))
			if v == "" {
				continue
			}
			if v == lower {
				isExact = true
			} else if strings.Contains(v, lower) {
				isPartial = true
			}
		}
		if isExact {
			exact = append(exact, m)
		} else if isPartial {
			partial = append(partial, m)
		}
	}
	for _, bucket := range [][]map[string]any{exact, partial} {
		switch len(bucket) {
		case 0:
			continue
		case 1:
			return asStr(bucket[0]["id"]), nil
		default:
			names := make([]string, len(bucket))
			for i, m := range bucket {
				names[i] = asStr(m[labelField])
			}
			if lang == "zh" {
				return "", fmt.Errorf("%s %q 有歧义，匹配到：%s（请改用 id）", kindZH, ref, strings.Join(names, "、"))
			}
			return "", fmt.Errorf("%s %q is ambiguous: matches %s (use the id instead)", kindEN, ref, strings.Join(names, ", "))
		}
	}
	if lang == "zh" {
		return "", fmt.Errorf("没有匹配%s %q 的记录", kindZH, ref)
	}
	return "", fmt.Errorf("no %s matches %q", kindEN, ref)
}

// resolveUserRef resolves ref to a user id via GET /api/admin/users, which
// every command in this file already holds the "users" grant to call. A
// value that already looks like an id is trusted as-is and not looked up —
// a wrong id then surfaces as the endpoint's own 404, which is the correct
// answer for "that account is gone", not "no account is named that".
func resolveUserRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	q := url.Values{"q": {ref}, "limit": {"25"}}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/users?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	return resolveByFields(rt.Session.Lang, "user", "用户", ref, asSlice(asMap(data)["users"]), "username", "username")
}

// resolveMemberRef is resolveUserRef's counterpart for group-domain
// commands (cmd_groups.go): it resolves a username through
// GET /api/admin/member-options instead of /api/admin/users, so "group
// assign alice" only ever needs the "groups" grant the command already
// requires, never an extra "users" grant just to type a name instead of an
// id.
func resolveMemberRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	q := url.Values{"q": {ref}, "limit": {"25"}}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/member-options?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	return resolveByFields(rt.Session.Lang, "user", "用户", ref, asSlice(asMap(data)["users"]), "username", "username")
}

// resolveGroupRef resolves ref to a group id. It reads the selector list
// GET /api/admin/references exposes for exactly this purpose (§2.56 of
// API.md): that list is gated on "users,models,usage,security,settings,
// groups" — any one of those — so a command outside the groups domain
// (user edit --group, model create --groups, quota set --scope group) can
// resolve a group name without needing the "groups" grant itself, as long
// as it holds whatever grant it does hold in that gate list, which every
// caller in this package does. A groups-domain command still works even if
// the gate were ever narrowed, because an empty answer here falls back to
// GET /api/admin/groups, which "groups" itself always passes.
func resolveGroupRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/references", nil)
	if err != nil {
		return "", err
	}
	groups := asSlice(asMap(data)["groups"])
	if len(groups) == 0 {
		data2, _, err2 := rt.Call(http.MethodGet, "/api/admin/groups", nil)
		if err2 != nil {
			return "", err2
		}
		groups = asSlice(asMap(data2)["groups"])
	}
	return resolveByFields(rt.Session.Lang, "group", "分组", ref, groups, "name", "name")
}

// resolveProviderRef mirrors resolveGroupRef for providers, using the
// references list gated on CanAdmin("models") — which is why a "model
// create --provider" only needs the "models" grant, never "providers".
func resolveProviderRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/references", nil)
	if err != nil {
		return "", err
	}
	providers := asSlice(asMap(data)["providers"])
	if len(providers) == 0 {
		data2, _, err2 := rt.Call(http.MethodGet, "/api/admin/providers", nil)
		if err2 != nil {
			return "", err2
		}
		providers = asSlice(asMap(data2)["providers"])
	}
	return resolveByFields(rt.Session.Lang, "provider", "服务商", ref, providers, "name", "name")
}

// resolveModelRef mirrors resolveGroupRef for models, matching against
// either the display name or the upstream model id, using the references
// list gated on "groups,settings,security" — which is why a "group edit
// --models" only needs the "groups" grant.
func resolveModelRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/references", nil)
	if err != nil {
		return "", err
	}
	models := asSlice(asMap(data)["models"])
	if got, mErr := resolveByFields(rt.Session.Lang, "model", "模型", ref, models, "display_name", "display_name", "model_id"); mErr == nil {
		return got, nil
	}
	data2, _, err2 := rt.Call(http.MethodGet, "/api/admin/models", nil)
	if err2 != nil {
		return "", err2
	}
	return resolveByFields(rt.Session.Lang, "model", "模型", ref, asSlice(asMap(data2)["models"]), "display_name", "display_name", "model_id")
}

// generatePassword produces a random password for `user passwd --generate`.
// id.Secret already exists for exactly this — cryptographically random
// bytes, base64url-encoded — so a temporary credential is not a second
// place in the codebase that has an opinion about how to read crypto/rand.
func generatePassword() string { return id.Secret(18) }

// parseJSONBody validates raw as JSON and wraps it as a json.RawMessage so
// Runtime.Call's own json.Marshal writes it through byte for byte, instead
// of decoding and re-encoding a document a console command (model import,
// setting import) has no reason to understand the shape of — the server is
// the one thing here that actually validates it field by field.
func parseJSONBody(rt *Runtime, raw string) (json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if !json.Valid([]byte(raw)) {
		if rt.Session.Lang == "zh" {
			return nil, fmt.Errorf("不是合法的 JSON")
		}
		return nil, fmt.Errorf("not valid JSON")
	}
	return json.RawMessage(raw), nil
}

func requireRef(rt *Runtime, what string) (string, error) {
	if rt.NArg() == 0 {
		if rt.Session.Lang == "zh" {
			return "", fmt.Errorf("需要一个%s", what)
		}
		return "", fmt.Errorf("a %s is required", what)
	}
	return rt.Arg(0), nil
}

// ---------------------------------------------------------------------
// user list | show | edit | delete | passwd | keys | key-revoke | chats |
// transcript | cards
// ---------------------------------------------------------------------

func init() {
	registerCommand(Command{
		Name:    "user list",
		Group:   "accounts",
		Summary: Text{EN: "List accounts", ZH: "列出账户"},
		Usage:   "user list [--q TEXT] [--role ROLE] [--status STATUS] [--group REF] [--limit N] [--offset N]",
		Help: Text{
			EN: "Lists accounts, newest first. --q matches username, email, nickname and QQ. --group " +
				"accepts a group name or id.",
			ZH: "按创建时间倒序列出账户。--q 会匹配用户名、邮箱、昵称和 QQ。--group 可以是分组名称或 id。",
		},
		Flags: []Flag{
			{Name: "--q", Hint: Text{EN: "search text", ZH: "搜索关键字"}, Value: "TEXT"},
			{Name: "--role", Hint: Text{EN: "user, admin or super_admin", ZH: "user、admin 或 super_admin"}, Value: "ROLE"},
			{Name: "--status", Hint: Text{EN: "active or disabled", ZH: "active 或 disabled"}, Value: "STATUS"},
			{Name: "--group", Hint: Text{EN: "group name or id", ZH: "分组名称或 id"}, Value: "REF"},
			{Name: "--limit", Hint: Text{EN: "page size, default 50", ZH: "每页数量，默认 50"}, Value: "N", Default: "50"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
		},
		Examples:   []string{"user list --q alice", "user list --role admin --status active"},
		Permission: "users",
		Endpoints:  []string{"GET /api/admin/users"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			if v := rt.String("q"); v != "" {
				q.Set("q", v)
			}
			if v := rt.String("role"); v != "" {
				q.Set("role", v)
			}
			if v := rt.String("status"); v != "" {
				q.Set("status", v)
			}
			if v := rt.String("group"); v != "" {
				gid, err := resolveGroupRef(rt, v)
				if err != nil {
					return err
				}
				q.Set("group_id", gid)
			}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}

			data, _, err := rt.Call(http.MethodGet, "/api/admin/users?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			users := asSlice(asMap(data)["users"])
			rows := make([][]string, 0, len(users))
			for _, raw := range users {
				u := asMap(raw)
				rows = append(rows, []string{
					asStr(u["id"]), asStr(u["username"]), asStr(u["role"]), asStr(u["status"]),
					asStr(u["group_id"]), asStr(u["email"]), formatMS(u["created_at"]),
				})
			}
			return rt.Table([]string{"id", "username", "role", "status", "group_id", "email", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "user show",
		Group:   "accounts",
		Summary: Text{EN: "Show one account, its quota and its API keys", ZH: "显示单个账户、配额及其 API 密钥"},
		Usage:   "user show <id|username>",
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
		},
		Examples:   []string{"user show alice", "user show 01H8X…"},
		Permission: "users",
		Endpoints:  []string{"GET /api/admin/users/{id}", "GET /api/admin/users/{id}/keys"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/users/"+url.PathEscape(uid), nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			u := asMap(m["user"])
			jsonMode := rt.effectiveJSON()

			var permNames []string
			for _, p := range asSlice(u["admin_permissions"]) {
				permNames = append(permNames, asStr(p))
			}
			if err := rt.Fields([][2]string{
				{"id", asStr(u["id"])},
				{"username", asStr(u["username"])},
				{"nickname", asStr(u["nickname"])},
				{"email", asStr(u["email"])},
				{"email_verified", yesNo(asBoolVal(u["email_verified"]))},
				{"qq", asStr(u["qq"])},
				{"role", asStr(u["role"])},
				{"admin_permissions", strings.Join(permNames, ", ")},
				{"group_id", asStr(u["group_id"])},
				{"group_expires_at", formatMS(u["group_expires_at"])},
				{"status", asStr(u["status"])},
				{"api_restricted", yesNo(asBoolVal(u["api_restricted"]))},
				{"api_restricted_until", formatMS(u["api_restricted_until"])},
				{"api_restriction_source", asStr(u["api_restriction_source"])},
				{"created_at", formatMS(u["created_at"])},
				{"last_login_at", formatMS(u["last_login_at"])},
				{"last_active_at", formatMS(u["last_active_at"])},
				{"signup_ip", asStr(u["signup_ip"])},
			}); err != nil {
				return err
			}

			if !jsonMode {
				cards := asMap(m["cards"])
				fmt.Fprintf(rt.Out, "\ncards: %v available, %v used, %v expired, %v total\n",
					asNum(cards["available"]), asNum(cards["used"]), asNum(cards["expired"]), asNum(cards["total"]))

				usage := asMap(m["usage"])
				var qrows [][]string
				for _, raw := range asSlice(usage["windows"]) {
					w := asMap(raw)
					qrows = append(qrows, []string{
						asStr(w["kind"]), yesNo(asBoolVal(w["enforced"])),
						fmt.Sprint(asNum(w["used_requests"])), limitStr(w["limit_requests"]),
						fmt.Sprint(asNum(w["used_tokens"])), limitStr(w["limit_tokens"]),
						fmt.Sprintf("%.2f", asNum(w["used_credits"])), limitStr(w["limit_credits"]),
					})
				}
				fmt.Fprintln(rt.Out, "\nquota:")
				RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour,
					[]string{"window", "enforced", "req used", "req limit", "tok used", "tok limit", "cr used", "cr limit"}, qrows)
				fmt.Fprintln(rt.Out, "\nkeys:")
			}

			keysData, _, err := rt.Call(http.MethodGet, "/api/admin/users/"+url.PathEscape(uid)+"/keys", nil)
			if err != nil {
				return err
			}
			var krows [][]string
			for _, raw := range asSlice(asMap(keysData)["keys"]) {
				k := asMap(raw)
				krows = append(krows, []string{
					asStr(k["id"]), asStr(k["prefix"]), asStr(k["name"]),
					yesNo(asBoolVal(k["disabled"])), formatMS(k["expires_at"]), formatMS(k["last_used_at"]),
				})
			}
			return rt.Table([]string{"id", "prefix", "name", "disabled", "expires", "last_used"}, krows)
		},
	})

	registerCommand(Command{
		Name:    "user edit",
		Group:   "accounts",
		Summary: Text{EN: "Edit an account", ZH: "编辑账户"},
		Usage:   "user edit <id|username> [flags]",
		Help: Text{
			EN: "Only the flags you give are changed — an absent flag leaves that field alone. " +
				"--group '' clears group membership. --permissions replaces the whole grant list.",
			ZH: "只会修改你给出的选项，未给出的字段保持不变。--group '' 会清除分组成员身份。" +
				"--permissions 会整体替换权限列表。",
		},
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
		},
		Flags: []Flag{
			{Name: "--nickname", Hint: Text{EN: "display nickname, ≤32 chars", ZH: "昵称，≤32 字符"}, Value: "TEXT"},
			{Name: "--avatar", Hint: Text{EN: "avatar URL or data", ZH: "头像 URL 或数据"}, Value: "TEXT"},
			{Name: "--bio", Hint: Text{EN: "bio, ≤500 chars", ZH: "简介，≤500 字符"}, Value: "TEXT"},
			{Name: "--email", Hint: Text{EN: "email address", ZH: "邮箱地址"}, Value: "EMAIL"},
			{Name: "--qq", Hint: Text{EN: "QQ number, or empty to clear", ZH: "QQ 号，留空可清除"}, Value: "QQ"},
			{Name: "--role", Hint: Text{EN: "user, admin or super_admin", ZH: "user、admin 或 super_admin"}, Value: "ROLE"},
			{Name: "--permissions", Hint: Text{EN: "comma-separated grant list, replaces the current one", ZH: "逗号分隔的权限列表，整体替换"}, Value: "LIST"},
			{Name: "--group", Hint: Text{EN: "group name or id, or '' to clear", ZH: "分组名称或 id，'' 表示清除"}, Value: "REF"},
			{Name: "--group-expires-at", Hint: Text{EN: "membership expiry, epoch ms, 0 = permanent", ZH: "成员到期时间（毫秒时间戳），0 表示永久"}, Value: "MS"},
			{Name: "--status", Hint: Text{EN: "active or disabled", ZH: "active 或 disabled"}, Value: "STATUS"},
			{Name: "--api-restricted", Hint: Text{EN: "turn the per-account API brake on/off", ZH: "开启或关闭该账户的 API 限制"}, Value: "BOOL"},
			{Name: "--api-restriction-hours", Hint: Text{EN: "0-8760, 0 = no automatic expiry", ZH: "0-8760，0 表示不自动到期"}, Value: "N"},
		},
		Examples: []string{
			"user edit alice --status disabled",
			"user edit alice --role admin --permissions users,groups",
		},
		Permission: "users",
		Endpoints:  []string{"PATCH /api/admin/users/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}

			body := bodyBuilder{}
			body.str(rt, "nickname", "nickname")
			body.str(rt, "avatar", "avatar")
			body.str(rt, "bio", "bio")
			body.str(rt, "email", "email")
			body.str(rt, "qq", "qq")
			body.str(rt, "role", "role")
			if rt.Present("permissions") {
				body["admin_permissions"] = splitCSV(rt.String("permissions"))
			}
			if rt.Present("group") {
				if v := rt.String("group"); v == "" {
					body["group_id"] = ""
				} else if gid, gerr := resolveGroupRef(rt, v); gerr != nil {
					return gerr
				} else {
					body["group_id"] = gid
				}
			}
			body.int64v(rt, "group-expires-at", "group_expires_at")
			body.str(rt, "status", "status")
			body.boolv(rt, "api-restricted", "api_restricted")
			body.intv(rt, "api-restriction-hours", "api_restriction_hours")

			if len(body) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有需要修改的内容：请至少给出一个选项")
				}
				return rt.Errorf("nothing to change: give at least one flag")
			}

			data, _, err := rt.Call(http.MethodPatch, "/api/admin/users/"+url.PathEscape(uid), map[string]any(body))
			if err != nil {
				return err
			}
			u := asMap(asMap(data)["user"])
			return rt.Fields([][2]string{
				{"id", asStr(u["id"])}, {"username", asStr(u["username"])},
				{"role", asStr(u["role"])}, {"status", asStr(u["status"])},
				{"group_id", asStr(u["group_id"])}, {"updated_at", formatMS(u["updated_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "user delete",
		Group:   "accounts",
		Summary: Text{EN: "Delete an account and everything it owns", ZH: "删除账户及其全部数据"},
		Usage:   "user delete <id|username> --yes",
		Help: Text{
			EN: "Deletes the account's conversations, messages, attachments, sessions, preferences and " +
				"usage ledger. You cannot delete your own account, and the last active super admin " +
				"cannot be deleted.",
			ZH: "会删除该账户的对话、消息、附件、会话、偏好设置和用量记录。不能删除自己的账户，" +
				"也不能删除最后一位在职超级管理员。",
		},
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
		},
		Examples:    []string{"user delete alice --yes", "user delete 01H8X… -y"},
		Permission:  "users",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/users/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/users/"+url.PathEscape(uid), nil); err != nil {
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
		Name:    "user passwd",
		Group:   "accounts",
		Summary: Text{EN: "Reset an account's password", ZH: "重置账户密码"},
		Usage:   "user passwd <id|username> [--password NEW | --generate] --yes",
		Help: Text{
			EN: "Every session for the account is ended by this. Give exactly one of --password or " +
				"--generate. A generated password is shown once, in full — it is not stored anywhere " +
				"and cannot be recovered afterwards.",
			ZH: "此操作会结束该账户的全部登录会话。请只给出 --password 或 --generate 之一。" +
				"生成的密码只会完整显示一次，不会被保存，之后也无法找回。",
		},
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
		},
		Flags: []Flag{
			{Name: "--password", Hint: Text{EN: "the new password, 8-256 bytes", ZH: "新密码，8-256 字节"}, Value: "NEW", Sensitive: true},
			{Name: "--generate", Hint: Text{EN: "generate a random password instead", ZH: "改为生成随机密码"}},
		},
		Examples:    []string{"user passwd alice --generate --yes", "user passwd alice --password 'a-good-password' -y"},
		Permission:  "users",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/users/{id}/password"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}

			generated := rt.Present("generate")
			password := rt.String("password")
			switch {
			case generated:
				password = generatePassword()
			case password == "":
				if rt.Session.Lang == "zh" {
					return rt.Errorf("请给出 --password 或 --generate 之一")
				}
				return rt.Errorf("give exactly one of --password or --generate")
			}

			if _, _, err := rt.Call(http.MethodPost, "/api/admin/users/"+url.PathEscape(uid)+"/password",
				map[string]any{"new_password": password}); err != nil {
				return err
			}
			if generated {
				if rt.Session.Lang == "zh" {
					rt.Printf("已生成新密码，请立即转告用户（之后不会再显示）：%s\n", password)
				} else {
					rt.Printf("generated a new password — hand it to the user now, it will not be shown again: %s\n", password)
				}
				return nil
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("密码已更新。\n")
			} else {
				rt.Printf("password updated.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:       "user keys",
		Group:      "accounts",
		Summary:    Text{EN: "List an account's API keys", ZH: "列出账户的 API 密钥"},
		Usage:      "user keys <id|username>",
		Args:       []Arg{{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true}},
		Examples:   []string{"user keys alice", "user keys alice --json"},
		SeeAlso:    []string{"user key-revoke"},
		Permission: "users",
		Endpoints:  []string{"GET /api/admin/users/{id}/keys"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/users/"+url.PathEscape(uid)+"/keys", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["keys"]) {
				k := asMap(raw)
				rows = append(rows, []string{
					asStr(k["id"]), asStr(k["prefix"]), asStr(k["name"]),
					yesNo(asBoolVal(k["disabled"])), formatMS(k["expires_at"]), formatMS(k["last_used_at"]),
				})
			}
			return rt.Table([]string{"id", "prefix", "name", "disabled", "expires", "last_used"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "user key-revoke",
		Group:   "accounts",
		Summary: Text{EN: "Revoke one of an account's API keys", ZH: "吊销账户的一个 API 密钥"},
		Usage:   "user key-revoke <id|username> <key-id> --yes",
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
			{Name: "key-id", Hint: Text{EN: "the key's id, from user keys", ZH: "密钥 id，来自 user keys"}, Required: true},
		},
		Examples:    []string{"user key-revoke alice 01H9Z… --yes", "user key-revoke alice 01H9Z… -y"},
		SeeAlso:     []string{"user keys"},
		Permission:  "users",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/users/{id}/keys/{key}"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() < 2 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要账户和密钥 id")
				}
				return rt.Errorf("an account and a key id are required")
			}
			uid, err := resolveUserRef(rt, rt.Arg(0))
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/users/"+url.PathEscape(uid)+"/keys/"+url.PathEscape(rt.Arg(1)), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已吊销。\n")
			} else {
				rt.Printf("revoked.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "user chats",
		Group:   "accounts",
		Summary: Text{EN: "List an account's conversations", ZH: "列出账户的对话"},
		Usage:   "user chats <id|username> [--limit N] [--offset N]",
		Help: Text{
			EN: "Reading another account's conversations is logged on the server, the same as it is in the UI.",
			ZH: "查看他人对话会在服务器留下记录，与在管理界面中操作一致。",
		},
		Args: []Arg{{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true}},
		Flags: []Flag{
			{Name: "--limit", Hint: Text{EN: "page size, default 60", ZH: "每页数量，默认 60"}, Value: "N", Default: "60"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
		},
		Examples:   []string{"user chats alice", "user chats alice --limit 20"},
		SeeAlso:    []string{"user transcript"},
		Permission: "users",
		Endpoints:  []string{"GET /api/admin/users/{id}/conversations"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			q := url.Values{}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 60)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/users/"+url.PathEscape(uid)+"/conversations?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["conversations"]) {
				c := asMap(raw)
				rows = append(rows, []string{
					asStr(c["id"]), asStr(c["title"]), asStr(c["model_id"]),
					yesNo(asBoolVal(c["pinned"])), fmt.Sprint(asNum(c["message_count"])), formatMS(c["created_at"]),
				})
			}
			return rt.Table([]string{"id", "title", "model_id", "pinned", "messages", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "user transcript",
		Group:   "accounts",
		Summary: Text{EN: "Show one of an account's conversations in full", ZH: "完整显示账户的某一段对话"},
		Usage:   "user transcript <id|username> <conversation-id>",
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
			{Name: "conversation-id", Hint: Text{EN: "from user chats", ZH: "来自 user chats"}, Required: true},
		},
		Examples:   []string{"user transcript alice 01H9Z…", "user transcript alice 01H9Z… --json"},
		SeeAlso:    []string{"user chats"},
		Permission: "users",
		Endpoints:  []string{"GET /api/admin/users/{id}/conversations/{conversation}"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() < 2 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要账户和对话 id")
				}
				return rt.Errorf("an account and a conversation id are required")
			}
			uid, err := resolveUserRef(rt, rt.Arg(0))
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet,
				"/api/admin/users/"+url.PathEscape(uid)+"/conversations/"+url.PathEscape(rt.Arg(1)), nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			c := asMap(m["conversation"])
			jsonMode := rt.effectiveJSON()
			fields := [][2]string{
				{"id", asStr(c["id"])}, {"title", asStr(c["title"])}, {"model_id", asStr(c["model_id"])},
			}
			if pid := asStr(c["project_id"]); pid != "" {
				fields = append(fields, [2]string{"project_id", pid})
			}
			fields = append(fields, [][2]string{
				{"pinned", yesNo(asBoolVal(c["pinned"]))}, {"archived", yesNo(asBoolVal(c["archived"]))},
				{"messages", fmt.Sprint(asNum(c["message_count"]))},
				{"created_at", formatMS(c["created_at"])}, {"updated_at", formatMS(c["updated_at"])},
			}...)
			if err := rt.Fields(fields); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			fmt.Fprintln(rt.Out, "\nmessages:")
			var rows [][]string
			for _, raw := range asSlice(m["messages"]) {
				msg := asMap(raw)
				stats := asMap(msg["stats"])
				rows = append(rows, []string{
					fmt.Sprint(asNum(msg["seq"])), asStr(msg["role"]), asStr(msg["model_name"]),
					truncateForTable(asStr(msg["content"]), 60),
					fmt.Sprint(asNum(stats["input_tokens"])), fmt.Sprint(asNum(stats["output_tokens"])),
					formatMS(msg["created_at"]),
				})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour,
				[]string{"seq", "role", "model", "content", "in", "out", "created"}, rows)
			return nil
		},
	})

	registerCommand(Command{
		Name:    "user create",
		Group:   "accounts",
		Summary: Text{EN: "Create an account", ZH: "新建账户"},
		Usage:   "user create <username> [--password P] [--email E] [--qq Q] [--nickname N] [--role ROLE] [--permissions LIST] [--group REF]",
		Help: Text{
			EN: "For the cases registration cannot serve: a service account, or onboarding someone " +
				"while signups are closed. The registration switch, the per-address limit and the " +
				"signup throttle do not apply. Without --password the account has none and cannot " +
				"be signed into with one, which is what a service account wants. " +
				"--permissions only means anything with --role admin.",
			ZH: "用于注册流程覆盖不到的情况：给程序用的账户，或者注册关闭时手工开号。" +
				"注册开关、每地址上限和注册频率限制都不适用。不给 --password 就是没有密码、" +
				"不能用密码登录，给程序用的账户通常就要这样。--permissions 只在 --role admin 时有意义。",
		},
		Args: []Arg{{Name: "username", Hint: Text{EN: "the new account's name", ZH: "新账户的用户名"}, Required: true}},
		Flags: []Flag{
			{Name: "--password", Hint: Text{EN: "login password; omit for an account without one", ZH: "登录密码；不给则该账户没有密码"}, Value: "TEXT", Sensitive: true},
			{Name: "--email", Hint: Text{EN: "email address", ZH: "邮箱地址"}, Value: "EMAIL"},
			{Name: "--qq", Hint: Text{EN: "QQ number", ZH: "QQ 号"}, Value: "QQ"},
			{Name: "--nickname", Hint: Text{EN: "display nickname, ≤32 chars", ZH: "昵称，≤32 字符"}, Value: "TEXT"},
			{Name: "--role", Hint: Text{EN: "user, admin or super_admin; default user", ZH: "user、admin 或 super_admin，默认 user"}, Value: "ROLE", Default: "user"},
			{Name: "--permissions", Hint: Text{EN: "comma-separated grant list, admins only", ZH: "逗号分隔的权限列表，仅对管理员有效"}, Value: "LIST"},
			{Name: "--group", Hint: Text{EN: "group name or id", ZH: "分组名称或 id"}, Value: "REF"},
			{Name: "--status", Hint: Text{EN: "active or disabled; default active", ZH: "active 或 disabled，默认 active"}, Value: "STATUS", Default: "active"},
		},
		Examples: []string{
			"user create station-bot --password '…' --role admin --permissions users",
			"user create alice --password '…' --email alice@example.com",
		},
		Permission: "users",
		Endpoints:  []string{"POST /api/admin/users"},
		Run: func(_ context.Context, rt *Runtime) error {
			name, err := requireRef(rt, "the new account's name")
			if err != nil {
				return err
			}
			body := bodyBuilder{"username": name}
			body.str(rt, "password", "password")
			body.str(rt, "email", "email")
			body.str(rt, "qq", "qq")
			body.str(rt, "nickname", "nickname")
			body.str(rt, "role", "role")
			body.str(rt, "status", "status")
			if rt.Present("permissions") {
				body["admin_permissions"] = splitCSV(rt.String("permissions"))
			}
			if rt.Present("group") {
				gid, err := resolveGroupRef(rt, rt.String("group"))
				if err != nil {
					return err
				}
				body["group_id"] = gid
			}

			data, _, err := rt.Call(http.MethodPost, "/api/admin/users", map[string]any(body))
			if err != nil {
				return err
			}
			created := asMap(asMap(data)["user"])
			return rt.Table(
				[]string{"id", "username", "role", "status"},
				[][]string{{asStr(created["id"]), asStr(created["username"]), asStr(created["role"]), asStr(created["status"])}},
			)
		},
	})

	registerCommand(Command{
		Name:    "user cards",
		Group:   "accounts",
		Summary: Text{EN: "Grant an account trial cards", ZH: "向账户发放体验卡"},
		Usage:   "user cards <id|username> [--name NAME] [--windows WINS] [--cards N] [--card-days D] [--expires-at MS]",
		// Said "never expires" until 2026-09: card.clampDays turns 0 into 30, so
		// every grant without --card-days is a 30-day card. An operator who read
		// this and handed out a batch believed they were permanent.
		Help: Text{
			EN: "--expires-at, when given, wins over --card-days. Neither given means one card " +
				"expiring in 30 days; there is no way to grant one that never expires.",
			ZH: "若给出 --expires-at，则优先于 --card-days。两者都不给时，发放一张 30 天后过期的卡；" +
				"没有「永不过期」这个选项。",
		},
		Args: []Arg{{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true}},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "name or label for the card", ZH: "卡片名称或标识"}, Value: "NAME"},
			{Name: "--windows", Hint: Text{EN: "quota windows, e.g. 5h, 1w, 1m, 5h,1w, or full", ZH: "重置周期，如 5h、1w、1m、5h,1w 或 full"}, Value: "WINS"},
			{Name: "--cards", Hint: Text{EN: "how many to grant, default 1, max 10000", ZH: "发放数量，默认 1，最多 10000"}, Value: "N", Default: "1"},
			{Name: "--card-days", Hint: Text{EN: "days from now until expiry, 0-3650", ZH: "自现在起多少天过期，0-3650"}, Value: "D"},
			{Name: "--expires-at", Hint: Text{EN: "explicit expiry, epoch ms, wins over --card-days", ZH: "明确的到期时间（毫秒时间戳），优先于 --card-days"}, Value: "MS"},
		},
		Examples:   []string{"user cards alice --cards 3 --card-days 30", "user cards alice --name 'VIP Boost' --windows 5h --cards 1"},
		Permission: "users",
		Endpoints:  []string{"POST /api/admin/users/{id}/cards"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			if rt.Present("windows") {
				body["windows"] = splitCSV(rt.String("windows"))
			}
			body.intv(rt, "cards", "cards")
			body.intv(rt, "card-days", "card_days")
			body.int64v(rt, "expires-at", "expires_at")
			data, _, err := rt.Call(http.MethodPost, "/api/admin/users/"+url.PathEscape(uid)+"/cards", map[string]any(body))
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["cards"]) {
				c := asMap(raw)
				rows = append(rows, []string{asStr(c["id"]), asStr(c["name"]), formatWindows(c["windows"]), asStr(c["source"]), formatMS(c["expires_at"]), formatMS(c["created_at"])})
			}
			return rt.Table([]string{"id", "name", "windows", "source", "expires", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "user cards move",
		Group:   "accounts",
		Summary: Text{EN: "Move the expiry on cards an account holds", ZH: "改掉账户手上重置卡的到期时间"},
		Usage:   "user cards move <id|username> --expires-at MS [--ids ID,ID]",
		// Granting and moving are one request apart and opposite in effect, so
		// the help says which one this is before an operator finds out.
		Help: Text{
			EN: "Moves cards this account has not spent, expired ones included, onto --expires-at. " +
				"Nothing new is issued and a spent card is never moved. Without --ids, every unused card moves.",
			ZH: "把这个账户还没用掉的卡（包括已经过期的）改到 --expires-at，不会新发卡，也不会动已经用掉的卡。" +
				"不给 --ids 时，未使用的卡全部改期。",
		},
		Args: []Arg{{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true}},
		Flags: []Flag{
			{Name: "--expires-at", Hint: Text{EN: "new expiry, epoch ms, within 3650 days", ZH: "新的到期时间（毫秒时间戳），不超过 3650 天"}, Value: "MS"},
			{Name: "--ids", Hint: Text{EN: "only these card ids, comma separated", ZH: "只改这些卡 id，逗号分隔"}, Value: "ID,ID"},
		},
		Examples: []string{
			"user cards move alice --expires-at 1790000000000",
			"user cards move alice --expires-at 1790000000000 --ids 01ARZ3NDEKTSV4RRFFQ69G5FAV",
		},
		Permission: "users",
		Endpoints:  []string{"PATCH /api/admin/users/{id}/cards"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			// Refused here rather than sent: there is no sensible default
			// date to move cards to, and an omitted flag would otherwise
			// reach the server as a zero it reads as "in 1970".
			if !rt.Present("expires-at") {
				if rt.Session.Lang == "zh" {
					return fmt.Errorf("需要 --expires-at：改期没有默认日期")
				}
				return fmt.Errorf("--expires-at is required: there is no default date to move cards to")
			}
			body := bodyBuilder{}
			body.int64v(rt, "expires-at", "expires_at")
			if rt.Present("ids") {
				body["card_ids"] = splitCSV(rt.String("ids"))
			}
			data, _, err := rt.Call(http.MethodPatch, "/api/admin/users/"+url.PathEscape(uid)+"/cards", map[string]any(body))
			if err != nil {
				return err
			}
			return rt.Table([]string{"moved"}, [][]string{{asStr(asMap(data)["moved"])}})
		},
	})

	registerCommand(Command{
		Name:    "user cards drop",
		Group:   "accounts",
		Summary: Text{EN: "Take one unused card back off an account", ZH: "收回账户手上的一张未使用重置卡"},
		Usage:   "user cards drop <id|username> <card-id>",
		Help: Text{
			EN: "Deletes the card outright. Only an unused one: a spent card is the record of a " +
				"reset that already happened. The account is not told.",
			ZH: "直接删掉这张卡。只能删还没用掉的——已经用掉的卡是那次重置的记录。不会通知用户。",
		},
		Args: []Arg{
			{Name: "id|username", Hint: Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
			{Name: "card-id", Hint: Text{EN: "the card to delete", ZH: "要删掉的卡 id"}, Required: true},
		},
		Examples: []string{
			"user cards drop alice 01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"user cards drop 01M1VG5FEB5GSSPAYG0PGRWN6F 01ARZ3NDEKTSV4RRFFQ69G5FAV",
		},
		Permission: "users",
		Endpoints:  []string{"DELETE /api/admin/users/{id}/cards/{card}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := resolveUserRef(rt, ref)
			if err != nil {
				return err
			}
			cardID := rt.Arg(1)
			if cardID == "" {
				if rt.Session.Lang == "zh" {
					return fmt.Errorf("需要一个卡 id")
				}
				return fmt.Errorf("a card id is required")
			}
			_, _, err = rt.Call(http.MethodDelete,
				"/api/admin/users/"+url.PathEscape(uid)+"/cards/"+url.PathEscape(cardID), nil)
			return err
		},
	})
}

// truncateForTable clips display text by rune before it ever reaches
// RenderTable — used for message content, which can be arbitrarily long
// and would otherwise force every other column in the row down to the
// floor width just to make room for it.
func truncateForTable(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
