package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// The "credit" family is the caller's own allowance and cards — "usage" is
// already the admin family's noun for the instance-wide view, so this one is
// named for what an account holder actually has: credit to spend. Every
// route below is scoped to auth.MustUser(r.Context()) with no id in the
// path (see internal/quota/http.go, internal/usage/http.go,
// internal/card/http.go), so — like backup export/import in
// cmd_self_backup.go — there is no argument that could point a command here
// at a different account, and none is accepted.
func init() {
	registerCommand(Command{
		Name:    "credit show",
		Group:   "credit",
		Summary: Text{EN: "Show your allowance right now", ZH: "显示你当前的用量额度"},
		Usage:   "credit show",
		Help: Text{
			EN: "Windows sort tightest first, so the one about to refuse a request is the one you see " +
				"without scrolling rather than whichever the server happened to list first.",
			ZH: "各窗口按压力从大到小排列，最先出现的就是最快要把你挡下来的那个，而不是服务器" +
				"恰好排在前面的那个。",
		},
		Examples:   []string{"credit show", "credit show --json"},
		SeeAlso:    []string{"credit history", "credit cards"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/usage/me"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/usage/me", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			// Older builds never sent display; absolute is what they always did,
			// so an empty field here means that rather than "unknown".
			display := asStr(m["display"])
			if display == "" {
				display = "absolute"
			}
			if err := rt.Fields([][2]string{
				{"unlimited", yesNo(asBoolVal(m["unlimited"]))},
				{"display", display},
			}); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				// Fields already printed this same call's raw body above; a
				// second table built from it would just be the windows twice.
				return nil
			}

			type window struct {
				cells    []string
				pressure float64
				has      bool
			}
			raw := asSlice(m["windows"])
			windows := make([]window, 0, len(raw))
			for _, v := range raw {
				w := asMap(v)
				pressure, has := windowPressure(w)
				pct := "-"
				if has {
					pct = fmt.Sprintf("%.0f%%", pressure*100)
				}
				windows = append(windows, window{
					cells: []string{
						asStr(w["kind"]), yesNo(asBoolVal(w["enforced"])),
						fmt.Sprint(asNum(w["used_requests"])), limitStr(w["limit_requests"]),
						fmt.Sprint(asNum(w["used_tokens"])), limitStr(w["limit_tokens"]),
						fmt.Sprintf("%.2f", asNum(w["used_credits"])), limitStr(w["limit_credits"]),
						pct, formatMS(w["resets_at"]),
					},
					pressure: pressure,
					has:      has,
				})
			}
			sort.SliceStable(windows, func(i, j int) bool {
				// A window with no limit at all (has == false) is never the one
				// about to stop anybody, so it sorts after every window that has
				// a real ratio, regardless of the zero value pressure shares
				// with "just started, nothing used yet".
				if windows[i].has != windows[j].has {
					return windows[i].has
				}
				return windows[i].pressure > windows[j].pressure
			})

			rows := make([][]string, len(windows))
			for i, w := range windows {
				rows[i] = w.cells
			}
			fmt.Fprintln(rt.Out)
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour,
				[]string{"window", "enforced", "req used", "req limit", "tok used", "tok limit", "cr used", "cr limit", "pressure", "resets"}, rows)
			return nil
		},
	})

	registerCommand(Command{
		Name:    "credit history",
		Group:   "credit",
		Summary: Text{EN: "List your recent spend", ZH: "列出你最近的消费记录"},
		Usage:   "credit history [--limit N]",
		Help: Text{
			EN: "Newest first. The server caps this at 100 rows no matter what --limit asks for — it " +
				"answers \"what did I just spend\", not an export of the whole ledger.",
			ZH: "按时间倒序排列。无论 --limit 给多大，服务器最多只返回 100 行——它回答的是“最近花了什么”，" +
				"不是整本流水的导出。",
		},
		Flags: []Flag{
			{Name: "--limit", Hint: Text{EN: "page size, default and max 100", ZH: "每页数量，默认与上限均为 100"}, Value: "N", Default: "100"},
		},
		Examples:   []string{"credit history", "credit history --limit 20"},
		SeeAlso:    []string{"credit show"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/usage/me/history"},
		Run: func(_ context.Context, rt *Runtime) error {
			path := "/api/usage/me/history"
			if v := rt.IntOr("limit", 0); v > 0 {
				path += "?" + (url.Values{"limit": {strconv.Itoa(v)}}).Encode()
			}
			data, _, err := rt.Call(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			if !rt.effectiveJSON() {
				totals := asMap(m["totals"])
				fmt.Fprintf(rt.Out, "total: %v requests, %v tokens, %.2f credits, %v errors\n\n",
					asNum(totals["requests"]), asNum(totals["total_tokens"]), asNum(totals["credits"]), asNum(totals["errors"]))
			}
			turns := asSlice(m["turns"])
			rows := make([][]string, 0, len(turns))
			for _, v := range turns {
				turn := asMap(v)
				rows = append(rows, []string{
					asStr(turn["id"]), asStr(turn["model_name"]), asStr(turn["status"]),
					fmt.Sprint(asNum(turn["input_tokens"])), fmt.Sprint(asNum(turn["output_tokens"])),
					fmt.Sprintf("%.2f", asNum(turn["credits"])), formatMS(turn["started_at"]),
				})
			}
			return rt.Table([]string{"id", "model", "status", "in", "out", "credits", "started"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "credit cards",
		Group:   "credit",
		Summary: Text{EN: "List your unused reset cards", ZH: "列出你尚未使用的重置卡"},
		Usage:   "credit cards",
		Help: Text{
			EN: "Only cards still worth something: used, expired and never-existed cards are all just " +
				"absent, the same way a spent key is absent from user keys.",
			ZH: "只列出还有价值的卡：已使用、已过期或从未存在的卡都不会出现，与已吊销的密钥不会出现在" +
				" user keys 里是一个道理。",
		},
		Examples:   []string{"credit cards", "credit cards --json"},
		SeeAlso:    []string{"credit use", "credit redeem"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/usage/cards"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/usage/cards", nil)
			if err != nil {
				return err
			}
			cards := asSlice(asMap(data)["cards"])
			rows := make([][]string, 0, len(cards))
			for _, v := range cards {
				c := asMap(v)
				rows = append(rows, []string{asStr(c["id"]), asStr(c["source"]), formatMS(c["expires_at"]), formatMS(c["created_at"])})
			}
			return rt.Table([]string{"id", "source", "expires", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "credit use",
		Group:   "credit",
		Summary: Text{EN: "Spend one of your cards to reset your allowance", ZH: "使用一张卡，重置你的用量额度"},
		Usage:   "credit use <card-id> --yes",
		Help: Text{
			EN: "Irreversible: a spent card cannot be put back. Find the id with credit cards.",
			ZH: "不可撤销：卡一旦使用就无法恢复。可用 credit cards 查到 id。",
		},
		Args: []Arg{
			{Name: "card-id", Hint: Text{EN: "from credit cards", ZH: "来自 credit cards"}, Required: true},
		},
		Examples:    []string{"credit use 01H9Z… --yes", "credit use 01H9Z… -y"},
		SeeAlso:     []string{"credit cards"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"POST /api/usage/cards/{id}/use"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "card id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodPost, "/api/usage/cards/"+url.PathEscape(ref)+"/use", nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已使用。\n")
			} else {
				rt.Printf("used.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "credit redeem",
		Group:   "credit",
		Summary: Text{EN: "Redeem a code for a new card", ZH: "兑换一个兑换码以获得一张新卡"},
		Usage:   "credit redeem --code CODE [--turnstile TOKEN]",
		Help: Text{
			EN: "--turnstile only matters when the instance has turned the challenge on for redemption; " +
				"leave it out otherwise. Guessing at codes is rate-limited the same way signing in is, " +
				"and an unknown code and an expired one answer identically on purpose.",
			ZH: "只有实例为兑换开启了验证码时 --turnstile 才有意义，否则不必填写。猜测兑换码会像" +
				"登录一样被限速，且不存在的码与已过期的码会给出完全相同的回复，这是故意的。",
		},
		Flags: []Flag{
			{Name: "--code", Hint: Text{EN: "the redemption code", ZH: "兑换码"}, Value: "CODE", Sensitive: true},
			{Name: "--turnstile", Hint: Text{EN: "challenge token, only if the instance requires one", ZH: "验证码 token，仅在实例要求时需要"}, Value: "TOKEN", Sensitive: true},
		},
		Examples:   []string{"credit redeem --code SPRING2026", "credit redeem --code SPRING2026 --json"},
		SeeAlso:    []string{"credit cards"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/usage/redeem"},
		Run: func(_ context.Context, rt *Runtime) error {
			code := rt.String("code")
			if code == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --code")
				}
				return rt.Errorf("--code is required")
			}
			body := map[string]any{"code": code}
			if v := rt.String("turnstile"); v != "" {
				body["turnstile"] = v
			}
			data, _, err := rt.Call(http.MethodPost, "/api/usage/redeem", body)
			if err != nil {
				return err
			}
			c := asMap(asMap(data)["card"])
			return rt.Fields([][2]string{
				{"id", asStr(c["id"])},
				{"source", asStr(c["source"])},
				{"expires_at", formatMS(c["expires_at"])},
				{"created_at", formatMS(c["created_at"])},
			})
		},
	})
}

// windowPressure is web/src/api/usage.ts's windowPressure, ported: the
// tightest ratio across whichever dimensions this window actually limits, as
// 0-1. A window with no limit on any dimension (has == false) is not "0%
// pressure" — it is not measurable at all, and credit show's sort treats the
// two differently rather than showing an unenforced window as the safest one.
func windowPressure(w map[string]any) (pressure float64, has bool) {
	consider := func(used, limit any) {
		if limit == nil {
			return
		}
		lim := asNum(limit)
		if lim <= 0 {
			return
		}
		if r := asNum(used) / lim; !has || r > pressure {
			pressure, has = r, true
		}
	}
	consider(w["used_requests"], w["limit_requests"])
	consider(w["used_tokens"], w["limit_tokens"])
	consider(w["used_credits"], w["limit_credits"])
	return pressure, has
}
