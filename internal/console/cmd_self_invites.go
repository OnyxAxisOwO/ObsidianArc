package console

import (
	"context"
	"fmt"
	"net/http"
)

// An account's own personal invite code, who has joined through it and the
// reward it has earned; claiming somebody else's code — a partner link, or
// a batch code opened up for it — under its own name; the terminal
// counterpart to the invites panel in settings. Every route is
// /api/profile/invites*, never /api/admin/*, so Permission: Anyone is
// correct the same way it is in cmd_self_keys.go: an account can only ever
// see and regenerate its own code, and Claim only ever spends a use for
// whoever is signed in.

// inviteRewardLabel is the one column that mixes three different states
// into a single string, the same way `invite uses`' own "reward" column
// does for the administrator's view: a milestone actually paid ("+N
// cards"), a qualifying invite just counted toward the next one ("counted"),
// a skip reason recorded instead of silently dropped, or "-" while nothing
// has resolved yet (an unverified invitee, on an instance that requires
// verification).
func inviteRewardLabel(u map[string]any, lang string) string {
	if asBoolVal(u["counted"]) {
		if n := asNum(u["reward_cards"]); n > 0 {
			if lang == "zh" {
				return fmt.Sprintf("+%v 张重置卡", n)
			}
			return fmt.Sprintf("+%v cards", n)
		}
		if lang == "zh" {
			return "已计入"
		}
		return "counted"
	}
	if s := asStr(u["reward_skipped"]); s != "" {
		switch s {
		case "same_ip":
			if lang == "zh" {
				return "不符合奖励条件"
			}
			return "Ineligible for reward"
		case "limit":
			if lang == "zh" {
				return "邀请名额已用完"
			}
			return "Invite limit reached"
		case "disabled":
			if lang == "zh" {
				return "奖励当时已关闭"
			}
			return "Rewards were off"
		case "inviter_gone":
			if lang == "zh" {
				return "邀请人账号已注销"
			}
			return "Inviter’s account no longer exists"
		case "inviter_disabled":
			if lang == "zh" {
				return "邀请人账号已被禁用"
			}
			return "Inviter’s account is disabled"
		default:
			return s
		}
	}
	return "-"
}

// inviteesTable renders the "invitees" array both `me invite` and
// `me invite regenerate` answer with, in the same shape.
func inviteesTable(rt *Runtime, invitees []any) {
	fmt.Fprintln(rt.Out, "\ninvitees:")
	rows := make([][]string, 0, len(invitees))
	for _, raw := range invitees {
		u := asMap(raw)
		rows = append(rows, []string{
			asStr(u["username"]), asStr(u["nickname"]), formatMS(u["created_at"]), inviteRewardLabel(u, rt.Session.Lang),
		})
	}
	RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"username", "nickname", "joined", "reward"}, rows)
}

// printInvitePayload renders the one shape both routes answer with — see
// internal/invite/http.go's own payload comment for why GET and regenerate
// share it, and why it is built even when personal codes are switched off
// (reward_every/reward_cards/reward_card_days/next_reward_in describe the
// claim box above this section, not this account's own code). jsonMode's
// own Fields call already prints the server's raw body when --json is in
// effect, so the invitees table below is skipped then, the same way
// `group show` and `user show` skip their own trailing tables.
func printInvitePayload(rt *Runtime, data any) error {
	m := asMap(data)
	jsonMode := rt.effectiveJSON()

	code := asStr(m["code"])
	link := "-"
	if code != "" {
		// No location.origin in a terminal — this is the path a browser's
		// partner link would carry, for whoever is copying it into a chat
		// or an email themselves.
		link = "/register?invite=" + code
	} else {
		code = "-"
	}
	limit := "unlimited"
	if n := asNum(m["limit"]); n != 0 {
		limit = fmt.Sprint(n)
	}

	if err := rt.Fields([][2]string{
		{"enabled", yesNo(asBoolVal(m["enabled"]))},
		{"code", displayInviteCode(code)},
		{"link", link},
		{"used", fmt.Sprint(asNum(m["used"]))},
		{"limit", limit},
		{"counted", fmt.Sprint(asNum(m["counted"]))},
		{"reward_every", fmt.Sprint(asNum(m["reward_every"]))},
		{"next_reward_in", fmt.Sprint(asNum(m["next_reward_in"]))},
		{"reward_cards", fmt.Sprint(asNum(m["reward_cards"]))},
		{"reward_card_days", fmt.Sprint(asNum(m["reward_card_days"]))},
	}); err != nil {
		return err
	}
	if jsonMode {
		return nil
	}
	inviteesTable(rt, asSlice(m["invitees"]))
	return nil
}

func init() {
	registerCommand(Command{
		Name:    "me invite",
		Group:   "profile",
		Summary: Text{EN: "Show your personal invite code and who joined through it", ZH: "查看你的个人邀请码及通过它加入的人"},
		Usage:   "me invite",
		Help: Text{
			EN: "Only shows a code when the operator has turned personal invite codes on — the claim box " +
				"below it (see me invite claim) works either way. A code is created automatically the " +
				"first time this is run, if the account does not have a live one yet — the same as " +
				"opening the invites panel in settings does. \"used\" counts successful registrations " +
				"toward \"limit\" (unlimited when 0); the code itself keeps working past the limit for " +
				"other accounts' purposes, it just stops crediting you. \"counted\" is qualifying invites " +
				"so far; a reward of \"reward_cards\" pays out every \"reward_every\" of them, and " +
				"\"next_reward_in\" is how many more are needed (0 when the reward is switched off).",
			ZH: "只有在运营方开启了个人邀请码时才会显示邀请码——下面的领取入口（见 me invite claim）不受此" +
				"影响，随时可用。若账户还没有有效的邀请码，第一次执行本命令会自动创建一个——与打开设置中的" +
				"邀请码面板效果相同。“used” 是计入“limit”的成功注册数（0 表示不限）；超过上限后邀请码本身" +
				"仍可被使用，只是不再为你计数。“counted” 是目前已计入的有效邀请数；每满 “reward_every” 个" +
				"就发放一次 “reward_cards” 张卡，“next_reward_in” 是距下一次发放还差几个（奖励关闭时为 0）。",
		},
		Examples:   []string{"me invite", "me invite --json"},
		SeeAlso:    []string{"me invite regenerate", "me invite claim"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/profile/invites"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/profile/invites", nil)
			if err != nil {
				return err
			}
			return printInvitePayload(rt, data)
		},
	})

	registerCommand(Command{
		Name:    "me invite regenerate",
		Group:   "profile",
		Summary: Text{EN: "Replace your personal invite code with a new one", ZH: "为你换发一个新的个人邀请码"},
		Usage:   "me invite regenerate --yes",
		Help: Text{
			EN: "The old code stops working immediately — anyone holding a link built from it can no " +
				"longer register through it. Registrations it already brought in, and any reward " +
				"already granted for them, are not affected.",
			ZH: "旧邀请码立即失效——任何人拿着用它生成的链接都无法再用它注册。它此前已经带来的注册，" +
				"以及已经发放的奖励，都不受影响。",
		},
		Examples:    []string{"me invite regenerate --yes", "me invite regenerate -y"},
		SeeAlso:     []string{"me invite"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"POST /api/profile/invites/regenerate"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodPost, "/api/profile/invites/regenerate", nil)
			if err != nil {
				return err
			}
			return printInvitePayload(rt, data)
		},
	})

	registerCommand(Command{
		Name:    "me invite claim",
		Group:   "profile",
		Summary: Text{EN: "Claim a partner or batch code that allows existing accounts", ZH: "领取一个允许老用户使用的合作方或批量邀请码"},
		Usage:   "me invite claim <code>",
		Help: Text{
			EN: "Only works for a code an administrator opened up to existing accounts — a partner link " +
				"or a batch code explicitly marked for it (see invite create --partner); never a " +
				"personal code, and never one that only ever seats brand new registrations. Joins the " +
				"code's group fresh if the account is not already in one of its own, or extends the same " +
				"group's expiry if already a member of it on a trial; refuses rather than downgrading or " +
				"replacing a permanent membership, or one in any other group. One claim per account per " +
				"code, ever. Unknown, revoked, expired, used-up, personal and not-claimable codes all " +
				"answer the same way, on purpose — guessing at codes here is rate-limited the same way " +
				"signing in is.",
			ZH: "只对管理员开放给老用户使用的合作方或批量邀请码（见 invite create --partner）有效——从不" +
				"适用于个人邀请码，也不适用于只接受全新注册的码。若账户当前不在该码分组里，会加入这个分组；" +
				"若已经在这个分组的试用期内，则在原到期时间基础上顺延；若已经永久在该分组，或处于其他任何" +
				"分组，会被拒绝，绝不会降级或替换现有分组。每个码每个账户终生只能领取一次。不存在、已撤销、" +
				"已过期、已用完、个人码，以及不允许领取的码，一律给出同一个回复——这是故意的，猜测邀请码会" +
				"像登录一样被限速。",
		},
		Args:       []Arg{{Name: "code", Hint: Text{EN: "the invite code to claim", ZH: "要领取的邀请码"}, Required: true}},
		Examples:   []string{"me invite claim PARTNERX", "me invite claim ab12-cd34"},
		SeeAlso:    []string{"me invite", "invite create"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/profile/invites/claim"},
		Run: func(_ context.Context, rt *Runtime) error {
			code, err := requireRef(rt, "invite code")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/profile/invites/claim", map[string]any{"code": code})
			if err != nil {
				return err
			}
			m := asMap(data)
			groupName := asStr(m["group_name"])
			if groupName == "" {
				groupName = "-"
			}
			return rt.Fields([][2]string{
				{"group_id", asStr(m["group_id"])},
				{"group_name", groupName},
				{"days", fmt.Sprint(asNum(m["days"]))},
				{"expires_at", formatMS(m["expires_at"])},
			})
		},
	})
}
