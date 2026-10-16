package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

func init() {
	registerCommand(Command{
		Name:    "audit list",
		Group:   "instance",
		Summary: Text{EN: "List what administrators did", ZH: "列出管理员的操作记录"},
		Usage:   "audit list [--actor REF] [--action \"METHOD /api/admin/...\"] [--outcome ok|failed] [--target ID] [--since MS] [--limit N] [--offset N]",
		Help: Text{
			EN: "Every backoffice write, and every read of somebody else's conversations, newest first. " +
				"Commands typed here are on it too, marked console:web or console:ssh.",
			ZH: "后台的每一次写操作，以及每一次查看他人对话，按时间倒序。在这里输入的命令也会记录，标为 console:web 或 console:ssh。",
		},
		Flags: []Flag{
			{Name: "--actor", Hint: Text{EN: "one administrator, by ref", ZH: "按管理员筛选，可用引用"}, Value: "REF"},
			{Name: "--action", Hint: Text{EN: "method and route, as audit actions lists them", ZH: "方法加路由，与 audit actions 列出的一致"}, Value: "ACTION"},
			{Name: "--outcome", Hint: Text{EN: "ok or failed", ZH: "ok 或 failed"}, Value: "OUTCOME"},
			{Name: "--target", Hint: Text{EN: "the id the action was on", ZH: "操作对象的 id"}, Value: "ID"},
			{Name: "--since", Hint: Text{EN: "epoch ms", ZH: "毫秒时间戳"}, Value: "MS"},
			{Name: "--limit", Hint: Text{EN: "page size, 1-200, default 50", ZH: "每页数量，1-200，默认 50"}, Value: "N", Default: "50"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
		},
		Examples:   []string{"audit list --outcome failed", "audit list --actor alice --limit 20"},
		SeeAlso:    []string{"audit show", "audit actions"},
		Permission: "audit",
		Endpoints:  []string{"GET /api/admin/audit"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			for _, kv := range [][2]string{{"action", "action"}, {"outcome", "outcome"}, {"target", "target_id"}, {"since", "since"}} {
				if v := rt.String(kv[0]); v != "" {
					q.Set(kv[1], v)
				}
			}
			if v := rt.String("actor"); v != "" {
				uid, err := resolveUserRef(rt, v)
				if err != nil {
					return err
				}
				q.Set("actor_id", uid)
			}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/audit?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["entries"]) {
				e := asMap(raw)
				target := asStr(e["target_label"])
				if target == "" {
					target = asStr(e["target_id"])
				}
				rows = append(rows, []string{
					asStr(e["id"]), formatMS(e["at"]), asStr(e["actor_username"]),
					asStr(e["method"]) + " " + asStr(e["route"]), target,
					numberString(e["status"]), asStr(e["via"]),
				})
			}
			return rt.Table([]string{"id", "at", "actor", "action", "target", "status", "via"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "audit show",
		Group:   "instance",
		Summary: Text{EN: "Show one audit entry with its request and changes", ZH: "显示一条操作记录及其请求和改动"},
		Usage:   "audit show <id>",
		Args: []Arg{
			{Name: "id", Hint: Text{EN: "entry id, from audit list", ZH: "记录 id，来自 audit list"}, Required: true},
		},
		Examples:   []string{"audit show 01H8X…", "audit show 01H8X… --json"},
		SeeAlso:    []string{"audit list"},
		Permission: "audit",
		Endpoints:  []string{"GET /api/admin/audit/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			entryID := rt.Arg(0)
			if entryID == "" {
				return fmt.Errorf("an entry id is required")
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/audit/"+url.PathEscape(entryID), nil)
			if err != nil {
				return err
			}
			e := asMap(asMap(data)["entry"])
			pairs := [][2]string{
				{"at", formatMS(e["at"])},
				{"actor", asStr(e["actor_username"])},
				{"action", asStr(e["method"]) + " " + asStr(e["path"])},
				{"target", asStr(e["target_label"])},
				{"status", numberString(e["status"])},
				{"via", asStr(e["via"])},
				{"ip", asStr(e["ip"])},
			}
			for _, raw := range asSlice(e["changes"]) {
				c := asMap(raw)
				if secret, _ := c["secret"].(bool); secret {
					pairs = append(pairs, [2]string{asStr(c["field"]), "(changed)"})
					continue
				}
				pairs = append(pairs, [2]string{asStr(c["field"]), asStr(c["from"]) + " → " + asStr(c["to"])})
			}
			if request := asStr(e["request"]); request != "" {
				pairs = append(pairs, [2]string{"request", request})
			}
			return rt.Fields(pairs)
		},
	})

	registerCommand(Command{
		Name:       "audit actions",
		Group:      "instance",
		Summary:    Text{EN: "List the actions on the audit trail, with counts", ZH: "列出操作记录中出现过的操作及次数"},
		Usage:      "audit actions [--since MS]",
		Flags:      []Flag{{Name: "--since", Hint: Text{EN: "epoch ms", ZH: "毫秒时间戳"}, Value: "MS"}},
		Examples:   []string{"audit actions", "audit actions --since 1767225600000"},
		SeeAlso:    []string{"audit list"},
		Permission: "audit",
		Endpoints:  []string{"GET /api/admin/audit/facets"},
		Run: func(_ context.Context, rt *Runtime) error {
			path := "/api/admin/audit/facets"
			if v := rt.String("since"); v != "" {
				path += "?since=" + url.QueryEscape(v)
			}
			data, _, err := rt.Call(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["routes"]) {
				o := asMap(raw)
				rows = append(rows, []string{asStr(o["value"]), numberString(o["count"])})
			}
			return rt.Table([]string{"action", "count"}, rows)
		},
	})
}
