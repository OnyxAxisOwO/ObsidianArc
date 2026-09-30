package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Bonus bars: the administrator's half is the "bonus" family, the account's
// own is under "credit" beside its allowance. The rules are in
// docs/architecture/bonus-and-checkin.md.

// numberString prints a number the console's decoder handed over, exactly.
func numberString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func barRows(bars []any) [][]string {
	rows := make([][]string, 0, len(bars))
	for _, raw := range bars {
		b := asMap(raw)
		rows = append(rows, []string{
			asStr(b["id"]), asStr(b["name"]), asStr(b["kind"]), asStr(b["toggle_mode"]),
			yesNo(asBoolVal(b["active"])),
			numberString(b["granted"]), numberString(b["used"]), numberString(b["holders"]),
		})
	}
	return rows
}

// findBar reads the list and picks a bar by id or by exact name: there is no
// single-bar endpoint, and an operator remembers names.
func findBar(rt *Runtime, ref string) (map[string]any, error) {
	data, _, err := rt.Call(http.MethodGet, "/api/admin/bonus/bars", nil)
	if err != nil {
		return nil, err
	}
	var match map[string]any
	for _, raw := range asSlice(asMap(data)["bars"]) {
		b := asMap(raw)
		if asStr(b["id"]) == ref {
			return b, nil
		}
		if strings.EqualFold(asStr(b["name"]), ref) {
			if match != nil {
				return nil, rt.Errorf("more than one bonus is called %q; use its id", ref)
			}
			match = b
		}
	}
	if match == nil {
		return nil, rt.Errorf("no bonus matches %q", ref)
	}
	return match, nil
}

func barBody(rt *Runtime, base map[string]any) map[string]any {
	body := map[string]any{}
	for _, key := range []string{"name", "description", "kind", "toggle_mode", "default_on", "show_total", "model_ids", "default_expires_at", "active"} {
		if v, ok := base[key]; ok {
			body[key] = v
		}
	}
	bb := bodyBuilder(body)
	bb.str(rt, "name", "name")
	bb.str(rt, "description", "description")
	bb.str(rt, "kind", "kind")
	bb.str(rt, "mode", "toggle_mode")
	bb.boolv(rt, "default-on", "default_on")
	bb.boolv(rt, "show-total", "show_total")
	bb.boolv(rt, "active", "active")
	bb.int64v(rt, "expires-at", "default_expires_at")
	if rt.Present("models") {
		body["model_ids"] = splitCSV(rt.String("models"))
	}
	return body
}

var barFlags = []Flag{
	{Name: "--description", Hint: Text{EN: "what it is, shown to accounts", ZH: "说明，会显示给用户"}, Value: "TEXT"},
	{Name: "--kind", Hint: Text{EN: "bonus (default) or reserve — spent only after everything else", ZH: "bonus（默认）或 reserve——所有额度用完之后才动用"}, Value: "KIND"},
	{Name: "--mode", Hint: Text{EN: "user (the account chooses), on (spent first), off (spent last)", ZH: "user（用户自选）、on（强制启用）、off（强制关闭）"}, Value: "MODE"},
	{Name: "--default-on", Hint: Text{EN: "whether a user-mode bar starts switched on (true/false)", ZH: "用户自选的赠金条默认是否开启（true/false）"}, Value: "BOOL"},
	{Name: "--show-total", Hint: Text{EN: "whether accounts see the amounts (true/false)", ZH: "用户是否能看到总量（true/false）"}, Value: "BOOL"},
	{Name: "--models", Hint: Text{EN: "model ids it covers, comma-separated; empty for all", ZH: "适用的模型 id，逗号分隔；留空表示全部"}, Value: "IDS"},
	{Name: "--expires-at", Hint: Text{EN: "default expiry for grants, epoch ms; 0 for none", ZH: "发放时的默认到期时间（毫秒时间戳）；0 表示不过期"}, Value: "MS"},
}

func init() {
	registerCommand(Command{
		Name:       "bonus list",
		Group:      "bonus",
		Summary:    Text{EN: "List bonus bars", ZH: "列出赠金条"},
		Usage:      "bonus list",
		Examples:   []string{"bonus list", "bonus list --json"},
		SeeAlso:    []string{"bonus create", "bonus grant"},
		Permission: "usage",
		Endpoints:  []string{"GET /api/admin/bonus/bars"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/bonus/bars", nil)
			if err != nil {
				return err
			}
			return rt.Table([]string{"id", "name", "kind", "mode", "active", "granted", "used", "holders"},
				barRows(asSlice(asMap(data)["bars"])))
		},
	})

	registerCommand(Command{
		Name:    "bonus create",
		Group:   "bonus",
		Summary: Text{EN: "Add a bonus bar", ZH: "新建赠金条"},
		Usage:   "bonus create <name> [--kind K] [--mode M] [--default-on B] [--show-total B] [--models IDS] [--expires-at MS]",
		Help: Text{
			EN: "A bar is the rules; amounts are granted into it with `bonus grant`. Spent in credits, before the " +
				"windows when switched on, and after them when kept back.",
			ZH: "赠金条只是规则；数额用 `bonus grant` 发进去。单位是积分：开启时先于三个窗口动用，关闭（留作后备）时在窗口用完之后才动用。",
		},
		Args:       []Arg{{Name: "name", Hint: Text{EN: "the bar's name", ZH: "赠金条的名称"}, Required: true}},
		Flags:      barFlags,
		Examples:   []string{"bonus create 新用户礼包 --mode user --show-total true", "bonus create 备用额度 --kind reserve"},
		Permission: "usage",
		Endpoints:  []string{"POST /api/admin/bonus/bars"},
		Run: func(_ context.Context, rt *Runtime) error {
			name := rt.Arg(0)
			if name == "" {
				return rt.Errorf("a name is required")
			}
			body := barBody(rt, map[string]any{"name": name})
			data, _, err := rt.Call(http.MethodPost, "/api/admin/bonus/bars", body)
			if err != nil {
				return err
			}
			return rt.Table([]string{"id", "name", "kind", "mode", "active", "granted", "used", "holders"},
				barRows([]any{asMap(data)["bar"]}))
		},
	})

	registerCommand(Command{
		Name:       "bonus edit",
		Group:      "bonus",
		Summary:    Text{EN: "Change a bonus bar's rules", ZH: "修改赠金条的规则"},
		Usage:      "bonus edit <id|name> [--name N] [--kind K] [--mode M] [--default-on B] [--show-total B] [--active B] [--models IDS] [--expires-at MS]",
		Help:       Text{EN: "A rule change reaches every holder at once; amounts already granted are untouched.", ZH: "规则的改动对所有持有者立即生效；已经发出的数额不变。"},
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "the bar", ZH: "赠金条"}, Required: true}},
		Flags:      append([]Flag{{Name: "--name", Hint: Text{EN: "new name", ZH: "新名称"}, Value: "NAME"}, {Name: "--active", Hint: Text{EN: "false stops it being spent and shown (true/false)", ZH: "false 表示停止动用和显示（true/false）"}, Value: "BOOL"}}, barFlags...),
		Examples:   []string{"bonus edit 新用户礼包 --mode off", "bonus edit 备用额度 --active false"},
		Permission: "usage",
		Endpoints:  []string{"PUT /api/admin/bonus/bars/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			bar, err := findBar(rt, rt.Arg(0))
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPut, "/api/admin/bonus/bars/"+url.PathEscape(asStr(bar["id"])), barBody(rt, bar))
			if err != nil {
				return err
			}
			return rt.Table([]string{"id", "name", "kind", "mode", "active", "granted", "used", "holders"},
				barRows([]any{asMap(data)["bar"]}))
		},
	})

	registerCommand(Command{
		Name:        "bonus delete",
		Group:       "bonus",
		Summary:     Text{EN: "Delete a bonus bar and everything granted in it", ZH: "删除赠金条及其中所有已发放的数额"},
		Usage:       "bonus delete <id|name> --yes",
		Help:        Text{EN: "Deleting is for a bar made by mistake. To stop one being spent, `bonus edit --active false` keeps the history.", ZH: "删除用于建错了的赠金条。想让它停用，用 `bonus edit --active false`，历史会保留。"},
		Args:        []Arg{{Name: "id|name", Hint: Text{EN: "the bar", ZH: "赠金条"}, Required: true}},
		Examples:    []string{"bonus delete 测试 --yes", "bonus delete 01ARZ3NDEKTSV4RRFFQ69G5FAV --yes"},
		Permission:  "usage",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/bonus/bars/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			bar, err := findBar(rt, rt.Arg(0))
			if err != nil {
				return err
			}
			_, _, err = rt.Call(http.MethodDelete, "/api/admin/bonus/bars/"+url.PathEscape(asStr(bar["id"])), nil)
			return err
		},
	})

	registerCommand(Command{
		Name:    "bonus grant",
		Group:   "bonus",
		Summary: Text{EN: "Grant credits in a bonus bar", ZH: "向赠金条里发放积分"},
		Usage:   "bonus grant <id|name> --amount N (--all | --group ID | --users NAME,NAME) [--days D | --expires-at MS] [--note TEXT] --yes",
		Help: Text{
			EN: "All or nothing. Without --days or --expires-at the bar's default expiry applies. Each account is told.",
			ZH: "要么全发要么都不发。没有 --days 和 --expires-at 时用赠金条的默认到期时间。每个收到的账户都会收到通知。",
		},
		Args: []Arg{{Name: "id|name", Hint: Text{EN: "the bar", ZH: "赠金条"}, Required: true}},
		Flags: []Flag{
			{Name: "--amount", Hint: Text{EN: "credits per account", ZH: "每个账户的积分数"}, Value: "N"},
			{Name: "--all", Hint: Text{EN: "every account", ZH: "所有账户"}},
			{Name: "--group", Hint: Text{EN: "one group's id", ZH: "某个用户组的 id"}, Value: "ID"},
			{Name: "--users", Hint: Text{EN: "usernames, comma-separated", ZH: "用户名，逗号分隔"}, Value: "NAMES"},
			{Name: "--days", Hint: Text{EN: "days from now until it expires", ZH: "自现在起多少天后过期"}, Value: "D"},
			{Name: "--expires-at", Hint: Text{EN: "explicit expiry, epoch ms", ZH: "明确的到期时间（毫秒时间戳）"}, Value: "MS"},
			{Name: "--note", Hint: Text{EN: "a line the account sees", ZH: "给用户看的一句话"}, Value: "TEXT"},
		},
		Examples:    []string{"bonus grant 新用户礼包 --amount 50 --all --days 30 --yes", "bonus grant 备用额度 --amount 5 --users alice,bob --note 补偿 --yes"},
		Permission:  "usage",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/bonus/bars/{id}/grants"},
		Run: func(_ context.Context, rt *Runtime) error {
			bar, err := findBar(rt, rt.Arg(0))
			if err != nil {
				return err
			}
			body := bodyBuilder{}
			body.floatv(rt, "amount", "amount")
			body.str(rt, "note", "note")
			body.str(rt, "group", "group_id")
			body.intv(rt, "days", "days")
			body.int64v(rt, "expires-at", "expires_at")
			if rt.Present("all") {
				body["all"] = true
			}
			if rt.Present("users") {
				body["usernames"] = splitCSV(rt.String("users"))
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/bonus/bars/"+url.PathEscape(asStr(bar["id"]))+"/grants", map[string]any(body))
			if err != nil {
				return err
			}
			m := asMap(data)
			rt.Printf("granted %s credits to %s account(s); expires %s\n", numberString(m["amount"]), numberString(m["granted"]), formatMS(m["expires_at"]))
			return nil
		},
	})

	registerCommand(Command{
		Name:       "bonus grants",
		Group:      "bonus",
		Summary:    Text{EN: "List what has been granted in a bonus bar", ZH: "列出赠金条里已经发放的数额"},
		Usage:      "bonus grants <id|name> [--limit N] [--offset N]",
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "the bar", ZH: "赠金条"}, Required: true}},
		Flags:      []Flag{{Name: "--limit", Hint: Text{EN: "page size, default 50", ZH: "每页条数，默认 50"}, Value: "N"}, {Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的条数"}, Value: "N"}},
		Examples:   []string{"bonus grants 新用户礼包 --limit 20", "bonus grants 新用户礼包 --offset 20 --json"},
		Permission: "usage",
		Endpoints:  []string{"GET /api/admin/bonus/bars/{id}/grants"},
		Run: func(_ context.Context, rt *Runtime) error {
			bar, err := findBar(rt, rt.Arg(0))
			if err != nil {
				return err
			}
			q := url.Values{}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if offset := rt.IntOr("offset", 0); offset != 0 {
				q.Set("offset", strconv.Itoa(offset))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/bonus/bars/"+url.PathEscape(asStr(bar["id"]))+"/grants?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["grants"]) {
				g := asMap(raw)
				rows = append(rows, []string{asStr(g["id"]), asStr(g["username"]), numberString(g["amount"]), numberString(g["used"]),
					formatMS(g["expires_at"]), asStr(g["source"]), formatMS(g["created_at"])})
			}
			return rt.Table([]string{"id", "username", "amount", "used", "expires", "source", "granted"}, rows)
		},
	})

	registerCommand(Command{
		Name:        "bonus revoke",
		Group:       "bonus",
		Summary:     Text{EN: "Take back what is left of one grant", ZH: "收回某一笔赠金里没用完的部分"},
		Usage:       "bonus revoke <grant-id> --yes",
		Help:        Text{EN: "The row stays with its amount cut to what was spent. The account is not told.", ZH: "记录保留，数额改成已经用掉的部分。不会通知用户。"},
		Args:        []Arg{{Name: "grant-id", Hint: Text{EN: "from `bonus grants`", ZH: "来自 `bonus grants`"}, Required: true}},
		Examples:    []string{"bonus revoke 01ARZ3NDEKTSV4RRFFQ69G5FAV --yes", "bonus grants 新用户礼包 --json"},
		Permission:  "usage",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/bonus/grants/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			id := rt.Arg(0)
			if id == "" {
				return fmt.Errorf("a grant id is required")
			}
			data, _, err := rt.Call(http.MethodDelete, "/api/admin/bonus/grants/"+url.PathEscape(id), nil)
			if err != nil {
				return err
			}
			rt.Printf("took back %s credits\n", numberString(asMap(data)["revoked"]))
			return nil
		},
	})

	registerCommand(Command{
		Name:       "credit bonus",
		Group:      "credit",
		Summary:    Text{EN: "Show your bonus bars", ZH: "显示你的赠金条"},
		Usage:      "credit bonus",
		Examples:   []string{"credit bonus", "credit bonus --json"},
		SeeAlso:    []string{"credit bonus switch", "credit show"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/bonus"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/bonus", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["bars"]) {
				b := asMap(raw)
				left := fmt.Sprintf("%v%%", b["percent"])
				if b["remaining"] != nil {
					left = numberString(b["remaining"]) + " / " + numberString(b["total"])
				}
				rows = append(rows, []string{asStr(b["bar_id"]), asStr(b["name"]), asStr(b["kind"]), yesNo(asBoolVal(b["enabled"])), left, formatMS(b["expires_at"])})
			}
			return rt.Table([]string{"id", "name", "kind", "first", "left", "expires"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "credit bonus switch",
		Group:      "credit",
		Summary:    Text{EN: "Switch a bonus bar on or off", ZH: "开启或关闭一条赠金条"},
		Usage:      "credit bonus switch <id> <on|off>",
		Help:       Text{EN: "On means it is spent before your allowance; off means it waits until the allowance is spent. Only bars you may choose.", ZH: "开启表示先于你的额度动用；关闭表示等额度用完后才动用。只能开关允许你自选的赠金条。"},
		Args:       []Arg{{Name: "id", Hint: Text{EN: "from `credit bonus`", ZH: "来自 `credit bonus`"}, Required: true}, {Name: "on|off", Hint: Text{EN: "the new state", ZH: "新状态"}, Required: true}},
		Examples:   []string{"credit bonus switch 01ARZ3NDEKTSV4RRFFQ69G5FAV off", "credit bonus switch 01ARZ3NDEKTSV4RRFFQ69G5FAV on"},
		Permission: Anyone,
		Endpoints:  []string{"PUT /api/bonus/{id}/choice"},
		Run: func(_ context.Context, rt *Runtime) error {
			state := strings.ToLower(rt.Arg(1))
			if rt.Arg(0) == "" || (state != "on" && state != "off") {
				return rt.Errorf("usage: credit bonus switch <id> <on|off>")
			}
			_, _, err := rt.Call(http.MethodPut, "/api/bonus/"+url.PathEscape(rt.Arg(0))+"/choice", map[string]any{"enabled": state == "on"})
			return err
		},
	})
}
