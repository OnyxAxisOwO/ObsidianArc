package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// parseGrants turns "model-a:use,model-b:view" into the
// []{model_id,access} shape POST/PATCH /api/admin/groups and
// /api/admin/models both take for model_grants, resolving each left-hand
// side through resolveModelRef so a name works as well as an id.
func parseGrants(rt *Runtime, raw string) ([]map[string]any, error) {
	var out []map[string]any
	for _, entry := range splitCSV(raw) {
		ref, access, ok := strings.Cut(entry, ":")
		if !ok {
			access = "use"
		}
		mid, err := resolveModelRef(rt, strings.TrimSpace(ref))
		if err != nil {
			return nil, err
		}
		access = strings.TrimSpace(access)
		if access == "" {
			access = "use"
		}
		out = append(out, map[string]any{"model_id": mid, "access": access})
	}
	return out, nil
}

func init() {
	registerCommand(Command{
		Name:       "group list",
		Group:      "groups",
		Summary:    Text{EN: "List groups", ZH: "列出分组"},
		Usage:      "group list",
		Examples:   []string{"group list", "group list --json"},
		Permission: "groups",
		Endpoints:  []string{"GET /api/admin/groups"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/groups", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["groups"]) {
				g := asMap(raw)
				rows = append(rows, []string{
					asStr(g["id"]), asStr(g["name"]), fmt.Sprint(asNum(g["members"])),
					yesNo(asBoolVal(g["is_default"])), yesNo(asBoolVal(g["allow_all_models"])),
					yesNo(asBoolVal(g["api_access"])), fmt.Sprint(asNum(g["sort_order"])),
				})
			}
			return rt.Table([]string{"id", "name", "members", "default", "all_models", "api_access", "sort"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "group show",
		Group:      "groups",
		Summary:    Text{EN: "Show one group, its model grants and its members", ZH: "显示单个分组、模型授权及成员"},
		Usage:      "group show <id|name>",
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "group id or name", ZH: "分组 id 或名称"}, Required: true}},
		Examples:   []string{"group show Default", "group show 01H8X…"},
		Permission: "groups",
		Endpoints:  []string{"GET /api/admin/groups", "GET /api/admin/member-options"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "group id or name")
			if err != nil {
				return err
			}
			gid, err := resolveGroupRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/groups", nil)
			if err != nil {
				return err
			}
			var g map[string]any
			for _, raw := range asSlice(asMap(data)["groups"]) {
				if m := asMap(raw); asStr(m["id"]) == gid {
					g = m
					break
				}
			}
			if g == nil {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有这个分组。")
				}
				return rt.Errorf("no such group.")
			}
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"id", asStr(g["id"])}, {"name", asStr(g["name"])}, {"description", asStr(g["description"])},
				{"is_default", yesNo(asBoolVal(g["is_default"]))}, {"allow_all_models", yesNo(asBoolVal(g["allow_all_models"]))},
				{"api_access", yesNo(asBoolVal(g["api_access"]))}, {"allow_stats", yesNo(asBoolVal(g["allow_stats"]))},
				{"allow_delete_conversations", yesNo(asBoolVal(g["allow_delete_conversations"]))},
				{"allow_terminal", yesNo(asBoolVal(g["allow_terminal"]))},
				{"sandbox_profile_id", asStr(g["sandbox_profile_id"])},
				{"sort_order", fmt.Sprint(asNum(g["sort_order"]))}, {"members", fmt.Sprint(asNum(g["members"]))},
				{"created_at", formatMS(g["created_at"])}, {"updated_at", formatMS(g["updated_at"])},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}

			fmt.Fprintln(rt.Out, "\nmodel grants:")
			var grows [][]string
			for _, raw := range asSlice(g["model_grants"]) {
				mg := asMap(raw)
				grows = append(grows, []string{asStr(mg["model_id"]), asStr(mg["access"])})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"model_id", "access"}, grows)

			fmt.Fprintln(rt.Out, "\nmembers:")
			members, _, err := rt.Call(http.MethodGet, "/api/admin/member-options?"+url.Values{"group_id": {gid}, "limit": {"200"}}.Encode(), nil)
			if err != nil {
				return err
			}
			var mrows [][]string
			for _, raw := range asSlice(asMap(members)["users"]) {
				u := asMap(raw)
				mrows = append(mrows, []string{asStr(u["id"]), asStr(u["username"]), asStr(u["nickname"]), formatMS(u["group_expires_at"])})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"id", "username", "nickname", "expires"}, mrows)
			return nil
		},
	})

	registerCommand(Command{
		Name:    "group create",
		Group:   "groups",
		Summary: Text{EN: "Create a group", ZH: "创建分组"},
		Usage:   "group create --name NAME [flags]",
		Args:    []Arg{},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-40 characters, required", ZH: "1-40 字符，必填"}, Value: "NAME"},
			{Name: "--description", Hint: Text{EN: "≤200 characters", ZH: "≤200 字符"}, Value: "TEXT"},
			{Name: "--default", Hint: Text{EN: "make this the default group for new accounts", ZH: "设为新账户的默认分组"}},
			{Name: "--allow-all-models", Hint: Text{EN: "grant every model regardless of individual grants", ZH: "无视单独授权，允许使用全部模型"}},
			{Name: "--api-access", Hint: Text{EN: "allow API key use, default true", ZH: "允许使用 API 密钥，默认 true"}, Value: "BOOL", Default: "true"},
			{Name: "--stats", Hint: Text{EN: "allow viewing usage stats, default true", ZH: "允许查看用量统计，默认 true"}, Value: "BOOL", Default: "true"},
			{Name: "--delete-conversations", Hint: Text{EN: "allow deleting conversations, default true", ZH: "允许删除对话，默认 true"}, Value: "BOOL", Default: "true"},
			{Name: "--terminal", Hint: Text{EN: "allow members to open the terminal, default true", ZH: "允许成员使用终端，默认 true"}, Value: "BOOL", Default: "true"},
			{Name: "--sandbox-profile", Hint: Text{EN: "sandbox profile id for work-mode code runs, default none", ZH: "工作模式运行代码所用的沙箱配置 id，默认不启用"}, Value: "ID"},
			{Name: "--sort-order", Hint: Text{EN: "lower sorts first", ZH: "数值越小越靠前"}, Value: "N"},
			{Name: "--models", Hint: Text{EN: "comma list of model refs, granted 'use'", ZH: "逗号分隔的模型引用，授予 use"}, Value: "LIST"},
			{Name: "--grants", Hint: Text{EN: "comma list of model:access pairs, wins over --models", ZH: "逗号分隔的 model:access 对，优先于 --models"}, Value: "LIST"},
		},
		Examples:   []string{"group create --name Trial --description 'For evaluators'", "group create --name VIP --allow-all-models"},
		Permission: "groups",
		Endpoints:  []string{"POST /api/admin/groups"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !rt.Present("name") || rt.String("name") == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --name")
				}
				return rt.Errorf("--name is required")
			}
			body := bodyBuilder{}
			body["name"] = rt.String("name")
			body.str(rt, "description", "description")
			body.boolv(rt, "default", "is_default")
			body.boolv(rt, "allow-all-models", "allow_all_models")
			body.boolv(rt, "api-access", "api_access")
			body.boolv(rt, "stats", "allow_stats")
			body.boolv(rt, "delete-conversations", "allow_delete_conversations")
			body.boolv(rt, "terminal", "allow_terminal")
			if rt.Present("sandbox-profile") {
				body["sandbox_profile_id"] = rt.String("sandbox-profile")
			}
			body.intv(rt, "sort-order", "sort_order")

			if rt.Present("grants") {
				grants, err := parseGrants(rt, rt.String("grants"))
				if err != nil {
					return err
				}
				body["model_grants"] = grants
			} else if rt.Present("models") {
				var ids []string
				for _, ref := range splitCSV(rt.String("models")) {
					mid, err := resolveModelRef(rt, ref)
					if err != nil {
						return err
					}
					ids = append(ids, mid)
				}
				body["model_ids"] = ids
			}

			data, _, err := rt.Call(http.MethodPost, "/api/admin/groups", map[string]any(body))
			if err != nil {
				return err
			}
			g := asMap(asMap(data)["group"])
			return rt.Fields([][2]string{{"id", asStr(g["id"])}, {"name", asStr(g["name"])}, {"created_at", formatMS(g["created_at"])}})
		},
	})

	registerCommand(Command{
		Name:    "group edit",
		Group:   "groups",
		Summary: Text{EN: "Edit a group", ZH: "编辑分组"},
		Usage:   "group edit <id|name> [flags]",
		Help: Text{
			EN: "Only the flags you give are changed. --grants or --models, when given, replaces the " +
				"group's whole model grant set.",
			ZH: "只会修改你给出的选项。若给出 --grants 或 --models，会整体替换该分组的模型授权。",
		},
		Args: []Arg{{Name: "id|name", Hint: Text{EN: "group id or name", ZH: "分组 id 或名称"}, Required: true}},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-40 characters", ZH: "1-40 字符"}, Value: "NAME"},
			{Name: "--description", Hint: Text{EN: "≤200 characters", ZH: "≤200 字符"}, Value: "TEXT"},
			{Name: "--default", Hint: Text{EN: "make this the default group", ZH: "设为默认分组"}, Value: "BOOL"},
			{Name: "--allow-all-models", Hint: Text{EN: "grant every model", ZH: "允许使用全部模型"}, Value: "BOOL"},
			{Name: "--api-access", Hint: Text{EN: "allow API key use", ZH: "允许使用 API 密钥"}, Value: "BOOL"},
			{Name: "--stats", Hint: Text{EN: "allow viewing usage stats", ZH: "允许查看用量统计"}, Value: "BOOL"},
			{Name: "--delete-conversations", Hint: Text{EN: "allow deleting conversations", ZH: "允许删除对话"}, Value: "BOOL"},
			{Name: "--terminal", Hint: Text{EN: "allow members to open the terminal", ZH: "允许成员使用终端"}, Value: "BOOL"},
			{Name: "--sandbox-profile", Hint: Text{EN: "sandbox profile id, empty switches it off", ZH: "沙箱配置 id，留空即关闭"}, Value: "ID"},
			{Name: "--sort-order", Hint: Text{EN: "lower sorts first", ZH: "数值越小越靠前"}, Value: "N"},
			{Name: "--models", Hint: Text{EN: "comma list of model refs, replaces grants, all 'use'", ZH: "逗号分隔的模型引用，替换授权，均为 use"}, Value: "LIST"},
			{Name: "--grants", Hint: Text{EN: "comma list of model:access pairs, wins over --models", ZH: "逗号分隔的 model:access 对，优先于 --models"}, Value: "LIST"},
		},
		Examples:   []string{"group edit Trial --description 'Updated'", "group edit Trial --grants gpt-4o:use,claude:view"},
		Permission: "groups",
		Endpoints:  []string{"PATCH /api/admin/groups/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "group id or name")
			if err != nil {
				return err
			}
			gid, err := resolveGroupRef(rt, ref)
			if err != nil {
				return err
			}
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			body.str(rt, "description", "description")
			body.boolv(rt, "default", "is_default")
			body.boolv(rt, "allow-all-models", "allow_all_models")
			body.boolv(rt, "api-access", "api_access")
			body.boolv(rt, "stats", "allow_stats")
			body.boolv(rt, "delete-conversations", "allow_delete_conversations")
			body.boolv(rt, "terminal", "allow_terminal")
			if rt.Present("sandbox-profile") {
				body["sandbox_profile_id"] = rt.String("sandbox-profile")
			}
			body.intv(rt, "sort-order", "sort_order")

			if rt.Present("grants") {
				grants, err := parseGrants(rt, rt.String("grants"))
				if err != nil {
					return err
				}
				body["model_grants"] = grants
			} else if rt.Present("models") {
				var ids []string
				for _, r := range splitCSV(rt.String("models")) {
					mid, err := resolveModelRef(rt, r)
					if err != nil {
						return err
					}
					ids = append(ids, mid)
				}
				body["model_ids"] = ids
			}

			if len(body) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有需要修改的内容：请至少给出一个选项")
				}
				return rt.Errorf("nothing to change: give at least one flag")
			}

			data, _, err := rt.Call(http.MethodPatch, "/api/admin/groups/"+url.PathEscape(gid), map[string]any(body))
			if err != nil {
				return err
			}
			g := asMap(asMap(data)["group"])
			return rt.Fields([][2]string{{"id", asStr(g["id"])}, {"name", asStr(g["name"])}, {"updated_at", formatMS(g["updated_at"])}})
		},
	})

	registerCommand(Command{
		Name:    "group delete",
		Group:   "groups",
		Summary: Text{EN: "Delete a group, moving its members to the default group", ZH: "删除分组，其成员将移入默认分组"},
		Usage:   "group delete <id|name> --yes",
		Help: Text{
			EN: "The default group cannot be deleted, and neither can the last remaining group.",
			ZH: "默认分组不能删除，最后一个分组也不能删除。",
		},
		Args:        []Arg{{Name: "id|name", Hint: Text{EN: "group id or name", ZH: "分组 id 或名称"}, Required: true}},
		Examples:    []string{"group delete Trial --yes", "group delete Trial -y"},
		Permission:  "groups",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/groups/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "group id or name")
			if err != nil {
				return err
			}
			gid, err := resolveGroupRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodDelete, "/api/admin/groups/"+url.PathEscape(gid), nil)
			if err != nil {
				return err
			}
			movedTo := asStr(asMap(data)["moved_to"])
			if rt.Session.Lang == "zh" {
				rt.Printf("已删除，成员已移入分组 %s。\n", movedTo)
			} else {
				rt.Printf("deleted; members moved to group %s.\n", movedTo)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "group members",
		Group:   "groups",
		Summary: Text{EN: "List a group's members", ZH: "列出分组成员"},
		Usage:   "group members <id|name> [--q TEXT] [--limit N] [--offset N]",
		Args:    []Arg{{Name: "id|name", Hint: Text{EN: "group id or name", ZH: "分组 id 或名称"}, Required: true}},
		Flags: []Flag{
			{Name: "--q", Hint: Text{EN: "search within the group's members", ZH: "在该分组成员中搜索"}, Value: "TEXT"},
			{Name: "--limit", Hint: Text{EN: "page size, default 20", ZH: "每页数量，默认 20"}, Value: "N", Default: "20"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
		},
		Examples:   []string{"group members Trial", "group members Trial --q alice"},
		SeeAlso:    []string{"group assign"},
		Permission: "groups",
		Endpoints:  []string{"GET /api/admin/member-options"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "group id or name")
			if err != nil {
				return err
			}
			gid, err := resolveGroupRef(rt, ref)
			if err != nil {
				return err
			}
			q := url.Values{"group_id": {gid}}
			if v := rt.String("q"); v != "" {
				q.Set("q", v)
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
				rows = append(rows, []string{asStr(u["id"]), asStr(u["username"]), asStr(u["nickname"]), formatMS(u["group_expires_at"])})
			}
			return rt.Table([]string{"id", "username", "nickname", "expires"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "group assign",
		Group:   "groups",
		Summary: Text{EN: "Move accounts into a group", ZH: "将账户移入分组"},
		Usage:   "group assign <id|name> --users REF1,REF2,... [--expires-at MS]",
		Help: Text{
			EN: "Accepts 1-200 accounts, by id or username, comma-separated. --expires-at is the " +
				"membership expiry in epoch ms; 0 or omitted means permanent.",
			ZH: "最多可接受 200 个账户，以逗号分隔，可用 id 或用户名。--expires-at 为成员到期时间" +
				"（毫秒时间戳）；不给出或为 0 表示永久。",
		},
		Args: []Arg{{Name: "id|name", Hint: Text{EN: "group id or name", ZH: "分组 id 或名称"}, Required: true}},
		Flags: []Flag{
			{Name: "--users", Hint: Text{EN: "comma list of account ids or usernames", ZH: "逗号分隔的账户 id 或用户名"}, Value: "LIST"},
			{Name: "--expires-at", Hint: Text{EN: "membership expiry, epoch ms, 0 = permanent", ZH: "成员到期时间（毫秒时间戳），0 表示永久"}, Value: "MS"},
		},
		Examples:   []string{"group assign Trial --users alice,bob", "group assign Trial --users alice --expires-at 1780000000000"},
		Permission: "groups",
		Endpoints:  []string{"POST /api/admin/groups/{id}/members"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "group id or name")
			if err != nil {
				return err
			}
			gid, err := resolveGroupRef(rt, ref)
			if err != nil {
				return err
			}
			refs := splitCSV(rt.String("users"))
			if len(refs) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --users，至少一个账户")
				}
				return rt.Errorf("--users is required, at least one account")
			}
			var ids []string
			for _, r := range refs {
				uid, err := resolveMemberRef(rt, r)
				if err != nil {
					return err
				}
				ids = append(ids, uid)
			}
			body := bodyBuilder{"user_ids": ids}
			body.int64v(rt, "expires-at", "expires_at")
			data, _, err := rt.Call(http.MethodPost, "/api/admin/groups/"+url.PathEscape(gid)+"/members", map[string]any(body))
			if err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已更新 %v 个账户。\n", asNum(asMap(data)["updated"]))
			} else {
				rt.Printf("updated %v accounts.\n", asNum(asMap(data)["updated"]))
			}
			return nil
		},
	})
}
