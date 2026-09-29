package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Administering invite codes: minting an administrator's batch or a single
// named partner code, listing and searching every code that exists,
// revoking one, and reading back who used it. The account-facing half — a
// personal code and its own invitees — is cmd_self_invites.go; both talk to
// the same package, internal/invite, over different route prefixes.

// inviteCodeLen mirrors invite.generatedLen: it is not imported from that
// package because nothing else in this file needs the invite package at
// all, the same reasoning hasPermission in registry.go gives for its own
// small, deliberate copy of admin logic. Kept as a comment beside the
// literal below rather than a second unexported constant nobody would
// think to look for.
const inviteCodeLen = 8

// displayInviteCode puts the hyphen back for an eight-character generated
// code and returns anything else — a partner's chosen text — exactly as
// stored. Mirrors invite.Display(); see inviteCodeLen above for why this is
// a copy rather than an import.
func displayInviteCode(code string) string {
	if len(code) == inviteCodeLen {
		return code[:4] + "-" + code[4:]
	}
	return code
}

// normaliseInviteRef mirrors invite.Normalise(): upper-cased, with spaces
// and hyphens removed, so "ab12-cd34", "AB12 CD34" and "ab12cd34" all match
// the same row an operator typed any of them for.
func normaliseInviteRef(raw string) string {
	var out strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if r == ' ' || r == '-' {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// resolveInviteRef resolves ref to an invite code's id via
// GET /api/admin/invites, the same shape resolveUserRef gives account
// commands. The code text is tried first and an id only after: a custom
// code is 4-32 characters of the very alphabet ULIDs are spelled in, so a
// 26-character one looks exactly like an id, and trusting the look would
// send it to the server as an id no row has.
// "invite revoke ab12-cd34" and "invite revoke AB12CD34" both work.
func resolveInviteRef(rt *Runtime, ref string) (string, error) {
	norm := normaliseInviteRef(ref)
	q := url.Values{"kind": {"all"}, "status": {"all"}, "q": {norm}, "limit": {"50"}}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/invites?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	for _, raw := range asSlice(asMap(data)["codes"]) {
		c := asMap(raw)
		if asStr(c["code"]) == norm {
			return asStr(c["id"]), nil
		}
	}
	if id.Valid(ref) {
		return ref, nil
	}
	if rt.Session.Lang == "zh" {
		return "", fmt.Errorf("没有这个邀请码")
	}
	return "", fmt.Errorf("no such invite code")
}

// parseDateFlag reads an inclusive calendar date ("2006-01-02"): the code
// works through the whole of that day on the server's clock, and stops at
// the midnight after it. Midnight at its start would kill a partner's link
// on the very day they were told it still worked. Epoch milliseconds —
// the one shape --expires takes. Every other
// timestamp in this package is typed as a raw epoch-ms flag instead; this
// command alone also takes a plain date because an operator handing a
// partner a trial window thinks in days, not milliseconds since 1970.
func parseDateFlag(rt *Runtime, flag string) (int64, error) {
	raw := rt.String(flag)
	t, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	if err != nil {
		if rt.Session.Lang == "zh" {
			return 0, fmt.Errorf("--%s 需要 YYYY-MM-DD 格式的日期", flag)
		}
		return 0, fmt.Errorf("--%s needs a date in YYYY-MM-DD format", flag)
	}
	return t.AddDate(0, 0, 1).UnixMilli(), nil
}

// parseGroupDays reads --group-days, which is either one integer (a fixed
// trial length) or "N-M" (a uniformly random length drawn fresh for each
// registration, per invite.Store.Consume) — the same low/high pair
// group_days/group_days_max carries on the wire.
func parseGroupDays(rt *Runtime, raw string) (days, daysMax int, err error) {
	lo, hi, ranged := strings.Cut(raw, "-")
	days, convErr := strconv.Atoi(strings.TrimSpace(lo))
	if convErr != nil {
		return 0, 0, badGroupDays(rt)
	}
	if !ranged {
		return days, 0, nil
	}
	daysMax, convErr = strconv.Atoi(strings.TrimSpace(hi))
	if convErr != nil {
		return 0, 0, badGroupDays(rt)
	}
	return days, daysMax, nil
}

func badGroupDays(rt *Runtime) error {
	if rt.Session.Lang == "zh" {
		return fmt.Errorf("--group-days 需要一个整数，或 N-M 形式的区间")
	}
	return fmt.Errorf("--group-days needs an integer, or a range like N-M")
}

// inviteCodeRow renders one Code the same way in `invite list` and
// `invite create`'s own confirmation, so an operator sees the identical
// columns whichever command showed them the row.
//
// name is a partner's own label, never set for a batch or personal code —
// see invite.Code.Name — so it sits beside owner rather than replacing it:
// a partner code has neither an owner nor a name blank at once.
func inviteCodeRow(c map[string]any) []string {
	owner := "-"
	if asStr(c["owner_id"]) != "" {
		owner = asStr(c["owner_username"])
	}
	name := "-"
	if n := asStr(c["name"]); n != "" {
		name = n
	}
	group := "-"
	if asStr(c["group_id"]) != "" {
		group = asStr(c["group_name"])
	}
	maxUses := "unlimited"
	if n := asNum(c["max_uses"]); n != 0 {
		maxUses = fmt.Sprint(n)
	}
	return []string{
		asStr(c["id"]), displayInviteCode(asStr(c["code"])), asStr(c["kind"]), name, asStr(c["status"]),
		owner, group, fmt.Sprint(asNum(c["uses"])), fmt.Sprint(asNum(c["claims"])), maxUses,
		formatMS(c["expires_at"]), formatMS(c["created_at"]),
	}
}

var inviteListHeaders = []string{
	"id", "code", "kind", "name", "status", "owner", "group", "uses", "claims", "max_uses", "expires", "created",
}

func init() {
	registerCommand(Command{
		Name:    "invite list",
		Group:   "invites",
		Summary: Text{EN: "List invite codes", ZH: "列出邀请码"},
		Usage:   "invite list [--kind batch|partner|personal|all] [--status active|used_up|expired|revoked] [--search TEXT] [--limit N] [--offset N]",
		Help: Text{
			EN: "--kind filters by what a code is for: batch (a plain administrator-minted code), " +
				"partner (a named partner or sponsor code), personal (an account's own), or all. The " +
				"older admin (batch+partner, i.e. every code with no owner) and user (personal) aliases " +
				"still work, for a bookmarked script written before partner codes existed. --search " +
				"matches a code's text or its owner's username.",
			ZH: "--kind 按用途筛选：batch（管理员生成的普通码）、partner（合作方/赞助商命名码）、" +
				"personal（账户自己的个人码），或 all。旧的 admin（batch+partner，即无所有者的码）和 " +
				"user（personal）别名仍然可用，兼容合作方邀请码出现之前写的脚本。--search 会匹配邀请码" +
				"文本或其所有者的用户名。",
		},
		Flags: []Flag{
			{Name: "--kind", Hint: Text{EN: "batch, partner, personal or all, default all", ZH: "batch、partner、personal 或 all，默认全部"}, Value: "KIND"},
			{Name: "--status", Hint: Text{EN: "active, used_up, expired or revoked, default all", ZH: "active、used_up、expired 或 revoked，默认全部"}, Value: "STATUS"},
			{Name: "--search", Hint: Text{EN: "matches code text or owner username", ZH: "匹配邀请码文本或所有者用户名"}, Value: "TEXT"},
			{Name: "--limit", Hint: Text{EN: "page size, default 50", ZH: "每页数量，默认 50"}, Value: "N", Default: "50"},
			{Name: "--offset", Hint: Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N", Default: "0"},
		},
		Examples:   []string{"invite list --kind partner", "invite list --status active --search PARTNER"},
		SeeAlso:    []string{"invite create", "invite revoke", "invite stats"},
		Permission: "invites",
		Endpoints:  []string{"GET /api/admin/invites"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			if v := rt.String("kind"); v != "" {
				q.Set("kind", v)
			}
			if v := rt.String("status"); v != "" {
				q.Set("status", v)
			}
			if v := rt.String("search"); v != "" {
				q.Set("q", v)
			}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/invites?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			rows := make([][]string, 0)
			for _, raw := range asSlice(asMap(data)["codes"]) {
				rows = append(rows, inviteCodeRow(asMap(raw)))
			}
			if err := rt.Table(inviteListHeaders, rows); err != nil {
				return err
			}
			if !rt.effectiveJSON() {
				if rt.Session.Lang == "zh" {
					rt.Printf("\n共 %v 个\n", asNum(asMap(data)["total"]))
				} else {
					rt.Printf("\n%v total\n", asNum(asMap(data)["total"]))
				}
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:  "invite create",
		Group: "invites",
		Summary: Text{
			EN: "Mint one or more invite codes, or one named partner code",
			ZH: "生成一个或多个邀请码，或生成一个自定义合作码",
		},
		Usage: "invite create [--count N] [--code CUSTOM] [--uses N] [--days N | --expires YYYY-MM-DD] " +
			"[--group REF] [--group-days N | N-M] [--note TEXT] [--partner NAME [--no-existing]]",
		Help: Text{
			EN: "Without --code, --count generated codes are minted (default 1). --code names one " +
				"code instead — for a partner or sponsor link — and requires --count to be 1 or " +
				"absent; it is 4-32 characters of letters and digits, folded the same way any typed " +
				"code is (case and hyphens do not matter). --uses is how many registrations the code " +
				"accepts before it stops working; 0 means unlimited, the shape a partner link wants. " +
				"--group puts every account that registers with the code into that group; --group-days " +
				"is how many days the membership lasts, or a range N-M to draw a different random " +
				"length for each registration (a partner's '3-7 day trial'); 0 alone means permanent. " +
				"Give at most one of --days (from now) or --expires (a calendar date) for when the code " +
				"itself stops being redeemable; neither means it never expires.\n\n" +
				"--partner turns this into a named partner code rather than a plain batch one: it " +
				"requires --code, --group and a count of 1 (the default when --count is left out), and " +
				"it also makes the code claimable by an account that already exists — see me invite " +
				"claim — since a partner link is usually handed to people who already have an account " +
				"here as often as to new signups. --no-existing turns that back off for this one code, " +
				"for a partner link that should only ever seat brand new accounts.",
			ZH: "不给 --code 时，生成 --count 个随机邀请码（默认 1 个）。--code 改为生成一个指定文本的邀请码" +
				"——用于合作方或赞助商链接——此时 --count 必须是 1 或不给；长度 4-32，只能是字母和数字，" +
				"折叠规则与手动输入邀请码相同（大小写和连字符不影响匹配）。--uses 是该码能被使用几次后失效，" +
				"0 表示不限次数，合作链接通常这样设置。--group 会让用该码注册的账户加入该分组；--group-days " +
				"是成员身份的天数，也可以给一个区间 N-M，让每次注册各自抽取不同的随机天数（例如合作方的" +
				"「3-7 天试用」）；单独的 0 表示永久。--days（从现在起）和 --expires（具体日期）最多给一个，" +
				"控制邀请码本身何时失效；两者都不给表示永不失效。\n\n" +
				"--partner 把这个码变成一个命名的合作方码，而不是普通批量码：需要 --code、--group，且数量" +
				"必须是 1（不给 --count 时默认就是 1）；它还会让这个码可以被**已有账户**领取（见 me invite " +
				"claim）——合作方链接通常既发给新用户，也发给已经在用的老用户。--no-existing 为这一个码单独" +
				"关掉这一点，用于只想给全新账户用的合作方链接。",
		},
		Flags: []Flag{
			{Name: "--count", Hint: Text{EN: "how many to generate, 1-500, default 1", ZH: "生成数量，1-500，默认 1"}, Value: "N", Default: "1"},
			{Name: "--code", Hint: Text{EN: "one custom code instead of generating; only with count 1", ZH: "指定一个自定义码，而非自动生成；仅限数量为 1 时"}, Value: "CUSTOM"},
			{Name: "--uses", Hint: Text{EN: "max registrations, 0-100000, 0 = unlimited, default 1", ZH: "最多可注册次数，0-100000，0 表示不限，默认 1"}, Value: "N", Default: "1"},
			{Name: "--days", Hint: Text{EN: "code expires this many days from now", ZH: "邀请码在这么多天后失效"}, Value: "N"},
			{Name: "--expires", Hint: Text{EN: "code expires on this calendar date, YYYY-MM-DD", ZH: "邀请码在此日期失效，格式 YYYY-MM-DD"}, Value: "YYYY-MM-DD"},
			{Name: "--group", Hint: Text{EN: "group name or id new accounts join", ZH: "新账户将加入的分组名称或 id"}, Value: "REF"},
			{Name: "--group-days", Hint: Text{EN: "membership length in days, or N-M for a random range; requires --group", ZH: "成员身份天数，或 N-M 随机区间；需要 --group"}, Value: "N|N-M"},
			{Name: "--note", Hint: Text{EN: "an operator label, ≤200 characters", ZH: "备注，≤200 字符"}, Value: "TEXT"},
			{Name: "--partner", Hint: Text{EN: "the partner's name, 1-60 characters; makes this a partner code", ZH: "合作方名称，1-60 字符；使其成为合作方码"}, Value: "NAME"},
			{Name: "--no-existing", Hint: Text{EN: "with --partner, refuse existing accounts (partners allow them by default)", ZH: "配合 --partner 使用，禁止老用户领取（合作方默认允许）"}},
		},
		Examples: []string{
			"invite create --count 20 --uses 1 --note 'launch batch'",
			"invite create --code PARTNERX --uses 0 --group Trial --group-days 3-7 --note 'aff partner'",
			"invite create --partner 'Acme Corp' --code PARTNERX --uses 0 --group Trial --group-days 3-7",
		},
		SeeAlso:    []string{"invite list", "invite revoke"},
		Permission: "invites",
		Endpoints:  []string{"POST /api/admin/invites"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.Present("days") && rt.Present("expires") {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("--days 和 --expires 最多给一个")
				}
				return rt.Errorf("give at most one of --days or --expires")
			}

			body := bodyBuilder{}
			body.intv(rt, "count", "count")
			body.str(rt, "code", "code")
			// Defaulted here rather than left to the server's own zero
			// value: unlike a redemption code's cards (clamped server-side
			// to a minimum of 1), an invite code's max_uses=0 is a real,
			// deliberate "unlimited" — the shape a partner link wants — so
			// an operator who forgets --uses must not mint one by accident.
			body["max_uses"] = rt.IntOr("uses", 1)
			body.str(rt, "note", "note")

			switch {
			case rt.Present("expires"):
				ms, err := parseDateFlag(rt, "expires")
				if err != nil {
					return err
				}
				body["expires_at"] = ms
			case rt.Present("days"):
				body["expires_at"] = time.Now().Add(time.Duration(rt.Int("days")) * 24 * time.Hour).UnixMilli()
			}

			if rt.Present("group") {
				gid, err := resolveGroupRef(rt, rt.String("group"))
				if err != nil {
					return err
				}
				body["group_id"] = gid
			}
			if rt.Present("group-days") {
				days, daysMax, err := parseGroupDays(rt, rt.String("group-days"))
				if err != nil {
					return err
				}
				body["group_days"] = days
				body["group_days_max"] = daysMax
			}
			if rt.Present("partner") {
				// "partner" rather than invite.CodeKindPartner: this file
				// imports nothing from internal/invite at all, on purpose —
				// see inviteCodeLen above.
				body["kind"] = "partner"
				body["name"] = rt.String("partner")
			}
			if rt.Present("no-existing") {
				body["allow_existing"] = false
			}

			data, _, err := rt.Call(http.MethodPost, "/api/admin/invites", map[string]any(body))
			if err != nil {
				return err
			}
			rows := make([][]string, 0)
			for _, raw := range asSlice(asMap(data)["codes"]) {
				rows = append(rows, inviteCodeRow(asMap(raw)))
			}
			return rt.Table(inviteListHeaders, rows)
		},
	})

	registerCommand(Command{
		Name:    "invite revoke",
		Group:   "invites",
		Summary: Text{EN: "Revoke an invite code", ZH: "撤销一个邀请码"},
		Usage:   "invite revoke <code|id> --yes",
		Help: Text{
			EN: "Stops the code accepting new registrations. Idempotent — revoking an already-revoked " +
				"code leaves its original revocation moment alone. Accounts that already registered " +
				"through it keep their group membership and whatever reward was already granted.",
			ZH: "让该邀请码不再能被用于注册，操作是幂等的——对已撤销的邀请码再次撤销不会改变原来的撤销时间。" +
				"已经用它注册过的账户，其分组成员身份和已发放的奖励都不受影响。",
		},
		Args:        []Arg{{Name: "code|id", Hint: Text{EN: "invite code or its id, from invite list", ZH: "邀请码或其 id，来自 invite list"}, Required: true}},
		Examples:    []string{"invite revoke PARTNERX --yes", "invite revoke AB12-CD34 -y"},
		SeeAlso:     []string{"invite list"},
		Permission:  "invites",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/invites/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "invite code or id")
			if err != nil {
				return err
			}
			codeID, err := resolveInviteRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodDelete, "/api/admin/invites/"+url.PathEscape(codeID), nil)
			if err != nil {
				return err
			}
			c := asMap(data)["code"]
			return rt.Fields([][2]string{
				{"id", asStr(asMap(c)["id"])}, {"code", displayInviteCode(asStr(asMap(c)["code"]))},
				{"status", asStr(asMap(c)["status"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "invite uses",
		Group:   "invites",
		Summary: Text{EN: "List who has registered through an invite code", ZH: "列出通过某邀请码注册的账户"},
		Usage:   "invite uses <code|id>",
		Args:    []Arg{{Name: "code|id", Hint: Text{EN: "invite code or its id, from invite list", ZH: "邀请码或其 id，来自 invite list"}, Required: true}},
		Examples: []string{
			"invite uses PARTNERX",
			"invite uses PARTNERX --json",
		},
		SeeAlso:    []string{"invite list", "invite stats"},
		Permission: "invites",
		Endpoints:  []string{"GET /api/admin/invites/{id}/uses"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "invite code or id")
			if err != nil {
				return err
			}
			codeID, err := resolveInviteRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/invites/"+url.PathEscape(codeID)+"/uses", nil)
			if err != nil {
				return err
			}
			rows := make([][]string, 0)
			for _, raw := range asSlice(asMap(data)["uses"]) {
				u := asMap(raw)
				rewarded := "-"
				if asNum(u["rewarded_at"]) != 0 {
					if asStr(u["reward_skipped"]) != "" {
						rewarded = asStr(u["reward_skipped"])
					} else {
						rewarded = fmt.Sprintf("+%v cards", asNum(u["reward_cards"]))
					}
				}
				// via tells a fresh registration (invite_uses, which can earn
				// its inviter a reward) apart from an existing account's
				// claim (invite_claims, which never does) — see invite.Use.
				rows = append(rows, []string{
					asStr(u["user_id"]), asStr(u["username"]), asStr(u["nickname"]), asStr(u["via"]),
					fmt.Sprint(asNum(u["group_days"])), formatMS(u["created_at"]), rewarded,
				})
			}
			return rt.Table([]string{"user_id", "username", "nickname", "via", "group_days", "joined", "reward"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "invite stats",
		Group:      "invites",
		Summary:    Text{EN: "Show invite code usage at a glance", ZH: "查看邀请码使用概况"},
		Usage:      "invite stats",
		Examples:   []string{"invite stats", "invite stats --json"},
		SeeAlso:    []string{"invite list", "invite uses"},
		Permission: "invites",
		Endpoints:  []string{"GET /api/admin/invites/stats"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/invites/stats", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"active", fmt.Sprint(asNum(m["active"]))},
				{"uses_total", fmt.Sprint(asNum(m["uses_total"]))},
				{"uses_7d", fmt.Sprint(asNum(m["uses_7d"]))},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			fmt.Fprintln(rt.Out, "\ntop inviters:")
			var rows [][]string
			for _, raw := range asSlice(m["top_inviters"]) {
				t := asMap(raw)
				rows = append(rows, []string{
					asStr(t["user_id"]), asStr(t["username"]), asStr(t["nickname"]),
					fmt.Sprint(asNum(t["invites"])), fmt.Sprint(asNum(t["rewarded"])),
				})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"user_id", "username", "nickname", "invites", "rewarded"}, rows)

			fmt.Fprintln(rt.Out, "\ntop partners:")
			var partnerRows [][]string
			for _, raw := range asSlice(m["partners"]) {
				p := asMap(raw)
				partnerRows = append(partnerRows, []string{
					asStr(p["id"]), displayInviteCode(asStr(p["code"])), asStr(p["name"]),
					fmt.Sprint(asNum(p["registrations"])), fmt.Sprint(asNum(p["claims"])),
				})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"id", "code", "name", "registrations", "claims"}, partnerRows)
			return nil
		},
	})
}
