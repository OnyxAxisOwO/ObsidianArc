package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func init() {
	registerCommand(Command{
		Name:       "dash",
		Group:      "operations",
		Summary:    Text{EN: "Show the dashboard: counts, recent usage and top models/users", ZH: "显示仪表盘：统计数字、近期用量及模型/用户排行"},
		Usage:      "dash [--metric requests|tokens|credits]",
		Flags:      []Flag{{Name: "--metric", Hint: Text{EN: "ranks top_models/top_users; default credits", ZH: "用于排行 top_models/top_users；默认按 credits"}, Value: "METRIC"}},
		Examples:   []string{"dash", "dash --metric requests"},
		Permission: "dashboard",
		Endpoints:  []string{"GET /api/admin/dashboard"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			if v := rt.String("metric"); v != "" {
				q.Set("metric", v)
			}
			path := "/api/admin/dashboard"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			data, _, err := rt.Call(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			jsonMode := rt.effectiveJSON()
			counts := asMap(m["counts"])
			if err := rt.Fields([][2]string{
				{"users", fmt.Sprint(asNum(counts["users"]))}, {"active_users", fmt.Sprint(asNum(counts["active_users"]))},
				{"providers", fmt.Sprint(asNum(counts["providers"]))}, {"enabled_providers", fmt.Sprint(asNum(counts["enabled_providers"]))},
				{"models", fmt.Sprint(asNum(counts["models"]))}, {"enabled_models", fmt.Sprint(asNum(counts["enabled_models"]))},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			printTotals := func(label string, t map[string]any) {
				fmt.Fprintf(rt.Out, "\n%s: %v requests, %v tokens, %.2f credits, %v errors\n",
					label, asNum(t["requests"]), asNum(t["total_tokens"]), asNum(t["credits"]), asNum(t["errors"]))
			}
			printTotals("last 24h", asMap(m["last_24h"]))
			printTotals("last 7d", asMap(m["last_7d"]))

			fmt.Fprintln(rt.Out, "\ntop models:")
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"model", "requests", "tokens", "credits"}, breakdownRows(m["top_models"]))
			fmt.Fprintln(rt.Out, "\ntop users:")
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"user", "requests", "tokens", "credits"}, breakdownRows(m["top_users"]))
			return nil
		},
	})

	registerCommand(Command{
		Name:       "res",
		Group:      "operations",
		Summary:    Text{EN: "Show storage, memory and CPU usage", ZH: "显示存储、内存及 CPU 使用情况"},
		Usage:      "res",
		Help:       Text{EN: "cpu.percent and cpu.window_sec are absent on the first call after start — run it twice.", ZH: "cpu.percent 和 cpu.window_sec 在启动后首次调用时不存在——请运行两次。"},
		Examples:   []string{"res", "watch --interval 5s res"},
		Permission: "resources",
		Endpoints:  []string{"GET /api/admin/resources"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/resources", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			storage, mem, cpu := asMap(m["storage"]), asMap(m["memory"]), asMap(m["cpu"])
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"storage.held_bytes", fmt.Sprint(asNum(storage["held_bytes"]))}, {"storage.held_count", fmt.Sprint(asNum(storage["held_count"]))},
				{"storage.discarded_count", fmt.Sprint(asNum(storage["discarded_count"]))},
				{"memory.heap_bytes", fmt.Sprint(asNum(mem["heap_bytes"]))}, {"memory.sys_bytes", fmt.Sprint(asNum(mem["sys_bytes"]))},
				{"memory.gc_count", fmt.Sprint(asNum(mem["gc_count"]))}, {"memory.goroutines", fmt.Sprint(asNum(mem["goroutines"]))},
				{"cpu.cores", fmt.Sprint(asNum(cpu["cores"]))}, {"cpu.gomaxprocs", fmt.Sprint(asNum(cpu["gomaxprocs"]))},
				{"cpu.percent", limitStr(cpu["percent"])},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			fmt.Fprintln(rt.Out, "\nstorage by user:")
			var rows [][]string
			for _, raw := range asSlice(storage["by_user"]) {
				u := asMap(raw)
				rows = append(rows, []string{asStr(u["user_id"]), asStr(u["name"]), fmt.Sprint(asNum(u["count"])), fmt.Sprint(asNum(u["bytes"]))})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"user_id", "name", "count", "bytes"}, rows)
			return nil
		},
	})

	registerCommand(Command{
		Name:       "health status",
		Group:      "operations",
		Summary:    Text{EN: "Show model liveness and uptime", ZH: "显示模型存活状态及可用率"},
		Usage:      "health status [--hours N]",
		Flags:      []Flag{{Name: "--hours", Hint: Text{EN: "window, 1-720, default 24", ZH: "窗口时长，1-720，默认 24"}, Value: "N", Default: "24"}},
		Examples:   []string{"health status", "health status --hours 168"},
		SeeAlso:    []string{"health probe", "health reset"},
		Permission: "availability",
		Endpoints:  []string{"GET /api/admin/health"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{"hours": {strconv.Itoa(rt.IntOr("hours", 24))}}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/health?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["models"]) {
				m := asMap(raw)
				status := asMap(m["status"])
				rows = append(rows, []string{
					asStr(m["name"]), asStr(m["provider"]), yesNo(asBoolVal(m["enabled"])),
					asStr(status["state"]), fmt.Sprintf("%.1f%%", asNum(status["uptime"])*100),
					fmt.Sprint(asNum(status["samples"])), fmt.Sprint(asNum(status["failures_in_a_row"])),
				})
			}
			return rt.Table([]string{"name", "provider", "enabled", "state", "uptime", "samples", "fails_in_row"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "health probe",
		Group:   "operations",
		Summary: Text{EN: "Probe every model right now", ZH: "立即探测全部模型"},
		Usage:   "health probe",
		Help: Text{
			EN: "Probes every model, including disabled ones, 4 at a time with a 20s timeout each. This " +
				"streams progress from the server; the console prints a line per update and a summary " +
				"at the end.",
			ZH: "会探测全部模型（含已禁用的），每批 4 个，单个超时 20 秒。服务器会流式返回进度，" +
				"控制台每次更新打印一行，并在结束时输出汇总。",
		},
		Examples:   []string{"health probe", "health probe --json"},
		SeeAlso:    []string{"health status", "health reset"},
		Permission: "availability",
		Endpoints:  []string{"POST /api/admin/health/probe"},
		Run: func(_ context.Context, rt *Runtime) error {
			raw, _, err := rt.CallRaw(http.MethodPost, "/api/admin/health/probe", nil)
			if err != nil {
				return err
			}
			frames := ParseSSE(raw)
			if len(frames) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有收到探测进度")
				}
				return rt.Errorf("no probe progress was received")
			}
			for _, f := range frames {
				switch f.Event {
				case "progress", "done":
					rt.Printf("%s: %s\n", f.Event, string(f.Data))
				case "error":
					rt.Printf("error: %s\n", string(f.Data))
				}
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "health reset",
		Group:   "operations",
		Summary: Text{EN: "Clear probe history and re-enable auto-disabled models", ZH: "清除探测历史并重新启用自动禁用的模型"},
		Usage:   "health reset --yes",
		Help: Text{
			EN: "Deletes probe history and moves the uptime baseline forward, so a wide --hours window " +
				"afterwards still only shows post-reset samples.",
			ZH: "删除探测历史并将可用率基准前移；此后即使 --hours 设置很大，也只会显示重置之后的数据。",
		},
		Examples:    []string{"health reset --yes", "health reset -y"},
		SeeAlso:     []string{"health status", "health probe"},
		Permission:  "availability",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/health/reset"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodPost, "/api/admin/health/reset", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			var notReenabled []string
			for _, v := range asSlice(m["models_not_reenabled"]) {
				notReenabled = append(notReenabled, asStr(v))
			}
			return rt.Fields([][2]string{
				{"reset_at", formatMS(m["reset_at"])}, {"probes_cleared", fmt.Sprint(asNum(m["probes_cleared"]))},
				{"models_reenabled", fmt.Sprint(asNum(m["models_reenabled"]))}, {"models_not_reenabled", strings.Join(notReenabled, ", ")},
			})
		},
	})

	usageFilterFlags := []Flag{
		{Name: "--user", Hint: Text{EN: "filter to one account, by ref", ZH: "按账户筛选，可用引用"}, Value: "REF"},
		{Name: "--group", Hint: Text{EN: "filter to one group, by ref", ZH: "按分组筛选，可用引用"}, Value: "REF"},
		{Name: "--model", Hint: Text{EN: "filter to one model, by ref", ZH: "按模型筛选，可用引用"}, Value: "REF"},
		{Name: "--provider", Hint: Text{EN: "filter to one provider, by ref", ZH: "按服务商筛选，可用引用"}, Value: "REF"},
		{Name: "--status", Hint: Text{EN: "ok, error, aborted or rejected", ZH: "ok、error、aborted 或 rejected"}, Value: "STATUS"},
		{Name: "--since", Hint: Text{EN: "epoch ms; default last 30 days; 0 = all time", ZH: "毫秒时间戳；默认最近 30 天；0 表示全部时间"}, Value: "MS"},
		{Name: "--until", Hint: Text{EN: "epoch ms", ZH: "毫秒时间戳"}, Value: "MS"},
	}

	buildUsageQuery := func(rt *Runtime) (url.Values, error) {
		q := url.Values{}
		if v := rt.String("user"); v != "" {
			uid, err := resolveUserRef(rt, v)
			if err != nil {
				return nil, err
			}
			q.Set("user_id", uid)
		}
		if v := rt.String("group"); v != "" {
			gid, err := resolveGroupRef(rt, v)
			if err != nil {
				return nil, err
			}
			q.Set("group_id", gid)
		}
		if v := rt.String("model"); v != "" {
			mid, err := resolveModelRef(rt, v)
			if err != nil {
				return nil, err
			}
			q.Set("model_id", mid)
		}
		if v := rt.String("provider"); v != "" {
			pid, err := resolveProviderRef(rt, v)
			if err != nil {
				return nil, err
			}
			q.Set("provider_id", pid)
		}
		if v := rt.String("status"); v != "" {
			q.Set("status", v)
		}
		if rt.Present("since") {
			q.Set("since", rt.String("since"))
		}
		if v := rt.String("until"); v != "" {
			q.Set("until", v)
		}
		return q, nil
	}

	registerCommand(Command{
		Name:    "usage summary",
		Group:   "operations",
		Summary: Text{EN: "Show usage totals and breakdowns", ZH: "显示用量汇总及分项统计"},
		Usage:   "usage summary [filters] [--metric requests|tokens|credits]",
		Flags:   append(append([]Flag{}, usageFilterFlags...), Flag{Name: "--metric", Hint: Text{EN: "ranks the breakdowns; default credits", ZH: "用于排行分项统计；默认按 credits"}, Value: "METRIC"}),
		Examples: []string{
			"usage summary", "usage summary --since 0 --model gpt-4o",
		},
		SeeAlso:    []string{"usage breakdown", "usage rpm", "usage records"},
		Permission: "usage",
		Endpoints:  []string{"GET /api/admin/usage"},
		Run: func(_ context.Context, rt *Runtime) error {
			q, err := buildUsageQuery(rt)
			if err != nil {
				return err
			}
			if v := rt.String("metric"); v != "" {
				q.Set("metric", v)
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/usage?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			t := asMap(m["totals"])
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"requests", fmt.Sprint(asNum(t["requests"]))}, {"input_tokens", fmt.Sprint(asNum(t["input_tokens"]))},
				{"output_tokens", fmt.Sprint(asNum(t["output_tokens"]))}, {"total_tokens", fmt.Sprint(asNum(t["total_tokens"]))},
				{"credits", fmt.Sprintf("%.2f", asNum(t["credits"]))}, {"errors", fmt.Sprint(asNum(t["errors"]))},
				{"current_rpm", fmt.Sprint(asNum(m["current_rpm"]))},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			fmt.Fprintln(rt.Out, "\nby model:")
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"model", "requests", "tokens", "credits"}, breakdownRows(m["by_model"]))
			fmt.Fprintln(rt.Out, "\nby provider:")
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"provider", "requests", "tokens", "credits"}, breakdownRows(m["by_provider"]))
			fmt.Fprintln(rt.Out, "\nby user:")
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"user", "requests", "tokens", "credits"}, breakdownRows(m["by_user"]))
			return nil
		},
	})

	registerCommand(Command{
		Name:    "usage breakdown",
		Group:   "operations",
		Summary: Text{EN: "Rank usage along one dimension: who uses a model, what an account uses", ZH: "按一个维度排行用量：某个模型谁在用，某个账户在用什么"},
		Usage:   "usage breakdown <model|provider|user|group|status> [filters] [--metric requests|tokens|credits|users]",
		Args:    []Arg{{Name: "dimension", Hint: Text{EN: "model, provider, user, group or status", ZH: "model、provider、user、group 或 status"}, Required: true}},
		Flags: append(append([]Flag{}, usageFilterFlags...), Flag{
			Name:  "--metric",
			Hint:  Text{EN: "ranks the rows; default credits; users ranks by how many accounts", ZH: "排行依据；默认按 credits；users 按使用人数排"},
			Value: "METRIC",
		}),
		Examples: []string{
			"usage breakdown user --model gpt-4o",
			"usage breakdown model --user alice",
			"usage breakdown model --metric users --since 0",
		},
		SeeAlso:    []string{"usage summary", "usage records"},
		Permission: "usage",
		Endpoints:  []string{"GET /api/admin/usage/breakdown"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要维度：model、provider、user、group 或 status")
				}
				return rt.Errorf("a dimension is required: model, provider, user, group or status")
			}
			dimension := rt.Arg(0)
			q, err := buildUsageQuery(rt)
			if err != nil {
				return err
			}
			// The dimension is passed through rather than checked here: the
			// server's list is the one that decides, and a copy of it in this
			// file would be a second list to forget when the first one grows.
			q.Set("dimension", dimension)
			if v := rt.String("metric"); v != "" {
				q.Set("metric", v)
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/usage/breakdown?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			// Every row but an account's counts the accounts that used it —
			// how widely something is used, rather than how heavily. An
			// account's own row would always say one, so it counts the models
			// that account moves between instead.
			reach := "users"
			if dimension == "user" {
				reach = "models"
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["rows"]) {
				b := asMap(raw)
				label := asStr(b["label"])
				if label == "" {
					label = asStr(b["key"])
				}
				// Nicknames are not unique and two providers may each serve a
				// model of the same name; the detail is what tells those rows
				// apart.
				if detail := asStr(b["detail"]); detail != "" {
					label += " (" + detail + ")"
				}
				rows = append(rows, []string{
					label, fmt.Sprint(asNum(b["requests"])), fmt.Sprint(asNum(b["total_tokens"])),
					fmt.Sprintf("%.2f", asNum(b["credits"])), fmt.Sprint(asNum(b[reach])), formatMS(b["last_at"]),
				})
			}
			return rt.Table([]string{dimension, "requests", "tokens", "credits", reach, "last used"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "usage rpm",
		Group:      "operations",
		Summary:    Text{EN: "Show the current requests-per-minute", ZH: "显示当前每分钟请求数"},
		Usage:      "usage rpm [filters]",
		Flags:      usageFilterFlags,
		Examples:   []string{"usage rpm", "watch --interval 2s usage rpm"},
		SeeAlso:    []string{"usage summary"},
		Permission: "usage",
		Endpoints:  []string{"GET /api/admin/usage/rpm"},
		Run: func(_ context.Context, rt *Runtime) error {
			q, err := buildUsageQuery(rt)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/usage/rpm?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"rpm", fmt.Sprint(asNum(asMap(data)["rpm"]))}})
		},
	})

	registerCommand(Command{
		Name:    "usage records",
		Group:   "operations",
		Summary: Text{EN: "List individual usage records", ZH: "列出单条用量记录"},
		Usage:   "usage records [filters] [--limit N] [--offset N]",
		Flags: append(append([]Flag{}, usageFilterFlags...),
			Flag{Name: "--limit", Hint: Text{EN: "page size, default 50", ZH: "每页数量，默认 50"}, Value: "N", Default: "50"},
			Flag{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"}),
		Examples:   []string{"usage records --user alice", "usage records --status error --limit 20"},
		SeeAlso:    []string{"usage summary"},
		Permission: "usage",
		Endpoints:  []string{"GET /api/admin/usage/records"},
		Run: func(_ context.Context, rt *Runtime) error {
			q, err := buildUsageQuery(rt)
			if err != nil {
				return err
			}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/usage/records?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["records"]) {
				r := asMap(raw)
				rows = append(rows, []string{
					asStr(r["username"]), asStr(r["model_name"]), asStr(r["status"]),
					fmt.Sprint(asNum(r["total_tokens"])), fmt.Sprintf("%.2f", asNum(r["credits"])), formatMS(r["started_at"]),
				})
			}
			return rt.Table([]string{"user", "model", "status", "tokens", "credits", "started"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "usage reset",
		Group:   "operations",
		Summary: Text{EN: "Reset spend back to full for everyone, a group or one account", ZH: "将全体、某分组或单个账户的用量额度重置为满额"},
		Usage:   "usage reset --scope all|group|user [--id REF] --yes",
		Args:    []Arg{},
		Flags: []Flag{
			{Name: "--scope", Hint: Text{EN: "all, group or user, required", ZH: "all、group 或 user，必填"}, Value: "SCOPE"},
			{Name: "--id", Hint: Text{EN: "group or account ref; required unless --scope all", ZH: "分组或账户引用；除 --scope all 外均必填"}, Value: "REF"},
		},
		Examples:    []string{"usage reset --scope all --yes", "usage reset --scope user --id alice -y"},
		Permission:  "usage",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/usage/reset"},
		Run: func(_ context.Context, rt *Runtime) error {
			scope := rt.String("scope")
			body := bodyBuilder{"scope": scope}
			switch scope {
			case "user":
				uid, err := resolveUserRef(rt, rt.String("id"))
				if err != nil {
					return err
				}
				body["id"] = uid
			case "group":
				gid, err := resolveGroupRef(rt, rt.String("id"))
				if err != nil {
					return err
				}
				body["id"] = gid
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/usage/reset", map[string]any(body))
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"accounts", fmt.Sprint(asNum(asMap(data)["accounts"]))}})
		},
	})

	registerCommand(Command{
		Name:    "quota list",
		Group:   "operations",
		Summary: Text{EN: "List quota policies", ZH: "列出配额策略"},
		Usage:   "quota list",
		Help: Text{
			EN: "Shown rows are filtered to what you may see: global-scope rows need usage, group-scope " +
				"needs groups (or usage), user-scope needs users (or usage).",
			ZH: "展示的行会按你的权限过滤：global 范围需要 usage；group 范围需要 groups（或 usage）；" +
				"user 范围需要 users（或 usage）。",
		},
		Examples:   []string{"quota list", "quota list --json"},
		SeeAlso:    []string{"quota set", "quota delete"},
		Permission: "users,groups,usage",
		Endpoints:  []string{"GET /api/admin/quota/policies"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/quota/policies", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["policies"]) {
				p := asMap(raw)
				windows := asMap(p["windows"])
				var wins []string
				for k := range windows {
					wins = append(wins, k)
				}
				rows = append(rows, []string{
					asStr(p["scope"]), asStr(p["scope_id"]), limitStr(p["rpm"]), limitStr(p["tpm"]),
					strings.Join(wins, ","), formatMS(p["updated_at"]),
				})
			}
			return rt.Table([]string{"scope", "scope_id", "rpm", "tpm", "windows", "updated"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "quota set",
		Group:   "operations",
		Summary: Text{EN: "Set a quota policy", ZH: "设置配额策略"},
		Usage:   "quota set --scope global|group|user [--id REF] [--rpm N] [--tpm N] [--window 5h|1w|1m --enabled BOOL --requests N --tokens N --credits F]",
		Help: Text{
			EN: "The policy is replaced wholesale on the server, so this command reads the existing " +
				"policy first and carries every other window forward unchanged — only the --window " +
				"named here (if any) and --rpm/--tpm are touched. --clear-rpm / --clear-tpm set that " +
				"limit back to 'inherit'.",
			ZH: "服务器端策略是整体替换写入的，因此本命令会先读取现有策略，并原样保留其余窗口的设置——" +
				"只会改动这里指定的 --window（如果给出）以及 --rpm / --tpm。--clear-rpm / --clear-tpm " +
				"会把对应限制改回“继承”。",
		},
		Flags: []Flag{
			{Name: "--scope", Hint: Text{EN: "global, group or user, required", ZH: "global、group 或 user，必填"}, Value: "SCOPE"},
			{Name: "--id", Hint: Text{EN: "group or account ref; required unless --scope global", ZH: "分组或账户引用；除 --scope global 外均必填"}, Value: "REF"},
			{Name: "--rpm", Hint: Text{EN: "requests per minute", ZH: "每分钟请求数"}, Value: "N"},
			{Name: "--tpm", Hint: Text{EN: "tokens per minute", ZH: "每分钟 token 数"}, Value: "N"},
			{Name: "--clear-rpm", Hint: Text{EN: "unset rpm (inherit)", ZH: "取消 rpm（继承）"}},
			{Name: "--clear-tpm", Hint: Text{EN: "unset tpm (inherit)", ZH: "取消 tpm（继承）"}},
			{Name: "--window", Hint: Text{EN: "5h, 1w or 1m — which window the rest of these flags edit", ZH: "5h、1w 或 1m —— 以下选项作用于哪个窗口"}, Value: "KIND"},
			{Name: "--enabled", Hint: Text{EN: "turn the named window on/off", ZH: "开启或关闭该窗口"}, Value: "BOOL"},
			{Name: "--requests", Hint: Text{EN: "request limit for the named window", ZH: "该窗口的请求数限制"}, Value: "N"},
			{Name: "--tokens", Hint: Text{EN: "token limit for the named window", ZH: "该窗口的 token 限制"}, Value: "N"},
			{Name: "--credits", Hint: Text{EN: "credit limit for the named window", ZH: "该窗口的 credits 限制"}, Value: "F"},
			{Name: "--clear-window", Hint: Text{EN: "remove every limit on the named window", ZH: "清除该窗口的全部限制"}},
		},
		Examples: []string{
			"quota set --scope group --id Trial --window 5h --requests 200 --enabled true",
			"quota set --scope user --id alice --rpm 30",
		},
		Permission: "users,groups,usage",
		Endpoints:  []string{"GET /api/admin/quota/policies", "PUT /api/admin/quota/policies"},
		Run: func(_ context.Context, rt *Runtime) error {
			scope := rt.String("scope")
			var scopeID string
			switch scope {
			case "global":
				scopeID = ""
			case "user":
				uid, err := resolveUserRef(rt, rt.String("id"))
				if err != nil {
					return err
				}
				scopeID = uid
			case "group":
				gid, err := resolveGroupRef(rt, rt.String("id"))
				if err != nil {
					return err
				}
				scopeID = gid
			default:
				if rt.Session.Lang == "zh" {
					return rt.Errorf("--scope 必须是 global、group 或 user")
				}
				return rt.Errorf("--scope must be global, group or user")
			}

			existing, _, err := rt.Call(http.MethodGet, "/api/admin/quota/policies", nil)
			if err != nil {
				return err
			}
			var rpm, tpm any
			windows := map[string]any{}
			for _, raw := range asSlice(asMap(existing)["policies"]) {
				p := asMap(raw)
				if asStr(p["scope"]) == scope && asStr(p["scope_id"]) == scopeID {
					rpm, tpm = p["rpm"], p["tpm"]
					for k, v := range asMap(p["windows"]) {
						windows[k] = v
					}
					break
				}
			}
			if rt.Present("clear-rpm") {
				rpm = nil
			} else if rt.Present("rpm") {
				rpm = rt.Int("rpm")
			}
			if rt.Present("clear-tpm") {
				tpm = nil
			} else if rt.Present("tpm") {
				tpm = rt.Int("tpm")
			}

			if kind := rt.String("window"); kind != "" {
				if rt.Present("clear-window") {
					delete(windows, kind)
				} else {
					win := asMap(windows[kind])
					if win == nil {
						win = map[string]any{}
					}
					if rt.Present("enabled") {
						win["enabled"] = rt.Bool("enabled")
					}
					if rt.Present("requests") {
						win["requests"] = rt.Int("requests")
					}
					if rt.Present("tokens") {
						win["tokens"] = rt.Int("tokens")
					}
					if rt.Present("credits") {
						f, _ := strconv.ParseFloat(rt.String("credits"), 64)
						win["credits"] = f
					}
					windows[kind] = win
				}
			}

			body := map[string]any{"scope": scope, "scope_id": scopeID, "rpm": rpm, "tpm": tpm, "windows": windows}
			data, _, err := rt.Call(http.MethodPut, "/api/admin/quota/policies", body)
			if err != nil {
				return err
			}
			p := asMap(asMap(data)["policy"])
			return rt.Fields([][2]string{
				{"scope", asStr(p["scope"])}, {"scope_id", asStr(p["scope_id"])},
				{"rpm", limitStr(p["rpm"])}, {"tpm", limitStr(p["tpm"])}, {"updated_at", formatMS(p["updated_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "quota delete",
		Group:   "operations",
		Summary: Text{EN: "Delete a quota policy", ZH: "删除配额策略"},
		Usage:   "quota delete <global|group|user> [<ref>] --yes",
		Args: []Arg{
			{Name: "scope", Hint: Text{EN: "global, group or user", ZH: "global、group 或 user"}, Required: true},
			{Name: "ref", Hint: Text{EN: "group or account ref; omit for global", ZH: "分组或账户引用；global 时省略"}},
		},
		Examples:    []string{"quota delete user alice --yes", "quota delete global --yes"},
		Permission:  "users,groups,usage",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/quota/policies/{scope}"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 scope：global、group 或 user")
				}
				return rt.Errorf("a scope is required: global, group or user")
			}
			scope := rt.Arg(0)
			var scopeID string
			switch scope {
			case "global":
			case "user":
				uid, err := resolveUserRef(rt, rt.Arg(1))
				if err != nil {
					return err
				}
				scopeID = uid
			case "group":
				gid, err := resolveGroupRef(rt, rt.Arg(1))
				if err != nil {
					return err
				}
				scopeID = gid
			default:
				if rt.Session.Lang == "zh" {
					return rt.Errorf("scope 必须是 global、group 或 user")
				}
				return rt.Errorf("scope must be global, group or user")
			}
			q := url.Values{}
			if scopeID != "" {
				q.Set("scope_id", scopeID)
			}
			path := "/api/admin/quota/policies/" + url.PathEscape(scope)
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			if _, _, err := rt.Call(http.MethodDelete, path, nil); err != nil {
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
		Name:       "code list",
		Group:      "operations",
		Summary:    Text{EN: "List redemption codes", ZH: "列出兑换码"},
		Usage:      "code list",
		Examples:   []string{"code list", "code list --json"},
		SeeAlso:    []string{"code create", "code redemptions"},
		Permission: "codes",
		Endpoints:  []string{"GET /api/admin/codes"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/codes", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["codes"]) {
				c := asMap(raw)
				rows = append(rows, []string{
					asStr(c["id"]), asStr(c["code"]), asStr(c["name"]), formatWindows(c["windows"]),
					fmt.Sprint(asNum(c["cards"])), fmt.Sprint(asNum(c["claimed"])),
					fmt.Sprint(asNum(c["card_days"])), formatMS(c["expires_at"]), asStr(c["note"]),
				})
			}
			return rt.Table([]string{"id", "code", "name", "windows", "cards", "claimed", "card_days", "expires", "note"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "code create",
		Group:   "operations",
		Summary: Text{EN: "Mint one or more redemption codes", ZH: "生成一个或多个兑换码"},
		Usage:   "code create [--name NAME] [--windows WINS] [--code TEXT] [--cards N] [--card-days D] [--expires-at MS] [--note TEXT] [--count N]",
		Help: Text{
			EN: "Leave --code empty to generate it. --count mints several distinct codes in one call and " +
				"requires --code to be empty.",
			ZH: "留空 --code 即自动生成。--count 可一次生成多个不同的兑换码，此时 --code 必须留空。",
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "name or label for the minted card", ZH: "卡片名称"}, Value: "NAME"},
			{Name: "--windows", Hint: Text{EN: "quota windows, e.g. 5h, 1w, 1m, 5h,1w, or full", ZH: "重置周期，如 5h、1w、1m、5h,1w 或 full"}, Value: "WINS"},
			{Name: "--code", Hint: Text{EN: "the literal code; empty = generated", ZH: "字面兑换码；留空则自动生成"}, Value: "TEXT"},
			{Name: "--cards", Hint: Text{EN: "cards per code, 1-10000, default 1", ZH: "每个兑换码的卡数，1-10000，默认 1"}, Value: "N", Default: "1"},
			{Name: "--card-days", Hint: Text{EN: "how long each minted card lives, 0-3650", ZH: "每张卡的有效天数，0-3650"}, Value: "D"},
			{Name: "--expires-at", Hint: Text{EN: "when the code itself stops being redeemable, epoch ms", ZH: "兑换码本身的失效时间（毫秒时间戳）"}, Value: "MS"},
			{Name: "--note", Hint: Text{EN: "an operator label", ZH: "备注"}, Value: "TEXT"},
			{Name: "--count", Hint: Text{EN: "how many distinct codes to mint, max 200", ZH: "生成多少个不同的兑换码，最多 200"}, Value: "N", Default: "1"},
		},
		Examples:   []string{"code create --cards 5 --card-days 30", "code create --name 'VIP' --windows 5h,1w --count 20"},
		SeeAlso:    []string{"code list"},
		Permission: "codes",
		Endpoints:  []string{"POST /api/admin/codes"},
		Run: func(_ context.Context, rt *Runtime) error {
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			if rt.Present("windows") {
				body["windows"] = splitCSV(rt.String("windows"))
			}
			body.str(rt, "code", "code")
			body.intv(rt, "cards", "cards")
			body.intv(rt, "card-days", "card_days")
			body.int64v(rt, "expires-at", "expires_at")
			body.str(rt, "note", "note")
			body.intv(rt, "count", "count")
			data, _, err := rt.Call(http.MethodPost, "/api/admin/codes", map[string]any(body))
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["codes"]) {
				c := asMap(raw)
				rows = append(rows, []string{
					asStr(c["id"]), asStr(c["code"]), asStr(c["name"]), formatWindows(c["windows"]),
					fmt.Sprint(asNum(c["cards"])), formatMS(c["expires_at"]),
				})
			}
			return rt.Table([]string{"id", "code", "name", "windows", "cards", "expires"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "code redemptions",
		Group:      "operations",
		Summary:    Text{EN: "List who has redeemed a code", ZH: "列出已兑换某兑换码的用户"},
		Usage:      "code redemptions <code-id>",
		Args:       []Arg{{Name: "code-id", Hint: Text{EN: "from code list", ZH: "来自 code list"}, Required: true}},
		Examples:   []string{"code redemptions 01H8X…", "code redemptions 01H8X… --json"},
		SeeAlso:    []string{"code list"},
		Permission: "codes",
		Endpoints:  []string{"GET /api/admin/codes/{id}/redemptions"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "code id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/codes/"+url.PathEscape(ref)+"/redemptions", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["redemptions"]) {
				r := asMap(raw)
				rows = append(rows, []string{asStr(r["user_id"]), asStr(r["username"]), asStr(r["nickname"]), formatMS(r["redeemed_at"])})
			}
			return rt.Table([]string{"user_id", "username", "nickname", "redeemed"}, rows)
		},
	})

	registerCommand(Command{
		Name:        "code delete",
		Group:       "operations",
		Summary:     Text{EN: "Withdraw a code", ZH: "撤销一个兑换码"},
		Usage:       "code delete <code-id> --yes",
		Help:        Text{EN: "Cards already minted from the code are left alone; this only stops further redemptions.", ZH: "该码已发出的卡不受影响；此操作只是阻止后续再兑换。"},
		Args:        []Arg{{Name: "code-id", Hint: Text{EN: "from code list", ZH: "来自 code list"}, Required: true}},
		Examples:    []string{"code delete 01H8X… --yes", "code delete 01H8X… -y"},
		SeeAlso:     []string{"code list"},
		Permission:  "codes",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/codes/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "code id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/codes/"+url.PathEscape(ref), nil); err != nil {
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

	logFilterFlags := []Flag{
		{Name: "--channel", Hint: Text{EN: "web or api", ZH: "web 或 api"}, Value: "CHANNEL"},
		{Name: "--method", Hint: Text{EN: "HTTP method", ZH: "HTTP 方法"}, Value: "METHOD"},
		{Name: "--outcome", Hint: Text{EN: "ok or failed", ZH: "ok 或 failed"}, Value: "OUTCOME"},
		{Name: "--path", Hint: Text{EN: "substring match on the request path", ZH: "对请求路径做子串匹配"}, Value: "TEXT"},
		{Name: "--error-code", Hint: Text{EN: "exact error code", ZH: "精确的错误码"}, Value: "CODE"},
		{Name: "--user", Hint: Text{EN: "filter to one account, by ref", ZH: "按账户筛选，可用引用"}, Value: "REF"},
		{Name: "--model", Hint: Text{EN: "filter to one model, by ref", ZH: "按模型筛选，可用引用"}, Value: "REF"},
		{Name: "--status", Hint: Text{EN: "HTTP status code", ZH: "HTTP 状态码"}, Value: "N"},
		{Name: "--since", Hint: Text{EN: "epoch ms", ZH: "毫秒时间戳"}, Value: "MS"},
		{Name: "--until", Hint: Text{EN: "epoch ms", ZH: "毫秒时间戳"}, Value: "MS"},
		{Name: "--limit", Hint: Text{EN: "page size, default 50", ZH: "每页数量，默认 50"}, Value: "N", Default: "50"},
		{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
	}

	registerCommand(Command{
		Name:       "log list",
		Group:      "operations",
		Summary:    Text{EN: "List request log entries", ZH: "列出请求日志"},
		Usage:      "log list [filters]",
		Flags:      logFilterFlags,
		Examples:   []string{"log list --outcome failed", "log list --user alice --since 0"},
		SeeAlso:    []string{"log facets", "log prune"},
		Permission: "logs",
		Endpoints:  []string{"GET /api/admin/logs"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			for _, kv := range [][2]string{{"channel", "channel"}, {"method", "method"}, {"outcome", "outcome"}, {"path", "path"}, {"error-code", "error_code"}} {
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
			if v := rt.String("model"); v != "" {
				mid, err := resolveModelRef(rt, v)
				if err != nil {
					return err
				}
				q.Set("model_id", mid)
			}
			if v := rt.String("status"); v != "" {
				q.Set("status", v)
			}
			if v := rt.String("since"); v != "" {
				q.Set("since", v)
			}
			if v := rt.String("until"); v != "" {
				q.Set("until", v)
			}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/logs?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["entries"]) {
				e := asMap(raw)
				rows = append(rows, []string{
					formatMS(e["at"]), asStr(e["method"]), asStr(e["path"]), fmt.Sprint(asNum(e["status"])),
					asStr(e["username"]), asStr(e["error_code"]),
				})
			}
			return rt.Table([]string{"at", "method", "path", "status", "user", "error_code"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "log facets",
		Group:      "operations",
		Summary:    Text{EN: "Show request log facet counts", ZH: "显示请求日志的分面统计"},
		Usage:      "log facets [--since MS]",
		Flags:      []Flag{{Name: "--since", Hint: Text{EN: "epoch ms, default 0 (all time)", ZH: "毫秒时间戳，默认 0（全部时间）"}, Value: "MS"}},
		Examples:   []string{"log facets", "log facets --since 0"},
		SeeAlso:    []string{"log list"},
		Permission: "logs",
		Endpoints:  []string{"GET /api/admin/logs/facets"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			if v := rt.String("since"); v != "" {
				q.Set("since", v)
			}
			path := "/api/admin/logs/facets"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			data, _, err := rt.Call(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"total", fmt.Sprint(asNum(m["total"]))}, {"dropped", fmt.Sprint(asNum(m["dropped"]))},
				{"evicted", fmt.Sprint(asNum(m["evicted"]))}, {"oldest", formatMS(m["oldest"])},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			for _, section := range []string{"users", "models", "error_codes", "statuses"} {
				fmt.Fprintf(rt.Out, "\n%s:\n", section)
				var rows [][]string
				for _, raw := range asSlice(m[section]) {
					f := asMap(raw)
					rows = append(rows, []string{asStr(f["value"]), asStr(f["label"]), fmt.Sprint(asNum(f["count"]))})
				}
				RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"value", "label", "count"}, rows)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "log prune",
		Group:   "operations",
		Summary: Text{EN: "Delete old request log entries", ZH: "删除较早的请求日志"},
		Usage:   "log prune --days N --yes",
		Help: Text{
			EN: "This is the audit trail. --days 0 deletes the entire log — the number is required, on " +
				"purpose, so a bare 'log prune' can never wipe everything by accident.",
			ZH: "这是审计日志。--days 0 会删除全部日志——因此天数为必填项，以避免误执行 'log prune' " +
				"时清空全部记录。",
		},
		Flags:       []Flag{{Name: "--days", Hint: Text{EN: "0-3650; 0 means every entry", ZH: "0-3650；0 表示全部记录"}, Value: "N"}},
		Examples:    []string{"log prune --days 90 --yes", "log prune --days 0 -y"},
		SeeAlso:     []string{"log list"},
		Permission:  "logs",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/logs/prune"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !rt.Present("days") {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --days（0 表示全部）")
				}
				return rt.Errorf("--days is required (0 means everything)")
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/logs/prune", map[string]any{"days": rt.Int("days")})
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"removed", fmt.Sprint(asNum(asMap(data)["removed"]))}})
		},
	})
}

// breakdownRows renders a usage.Breakdown slice (dashboard's top_models /
// top_users, usage summary's by_model / by_provider / by_user) — they all
// share the same {key,label} + Totals shape once decoded generically.
func breakdownRows(v any) [][]string {
	var rows [][]string
	for _, raw := range asSlice(v) {
		b := asMap(raw)
		label := asStr(b["label"])
		if label == "" {
			label = asStr(b["key"])
		}
		rows = append(rows, []string{label, fmt.Sprint(asNum(b["requests"])), fmt.Sprint(asNum(b["total_tokens"])), fmt.Sprintf("%.2f", asNum(b["credits"]))})
	}
	return rows
}
