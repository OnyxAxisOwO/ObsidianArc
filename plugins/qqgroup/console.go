package qqgroup

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
)

// oauthBinding is the community sign-in's shape: an OpenID Connect provider
// whose subject is the member's QQ number. Anything that is not a QQ
// number's shape is some other identity provider and is left alone.
func oauthBinding() oauth.SubjectBinding {
	return oauth.SubjectBinding{Provider: "oidc", Field: Field, Matches: ValidQQ}
}

// The console's commands for this plugin's two backoffice routes. They live
// here rather than in internal/console because the routes do: the core's
// route-parity test reads the core's table, and this is the parity for
// these two.
func init() {
	console.Register(console.Command{
		Name:    "user depart",
		Group:   "accounts",
		Summary: console.Text{EN: "Process a group departure for an account", ZH: "处理账户的退群"},
		Usage:   "user depart <id|username> [--mode=disable|delete] [--note TEXT] --yes",
		Help: console.Text{
			EN: "Ends the account the way a member leaving the QQ group is handled, and takes back " +
				"the reset cards their invite earned. --mode=disable keeps the account (reversible; " +
				"the QQ number stays taken); --mode=delete removes it and everything cascading from " +
				"it. Cards already spent are not taken from anywhere else — the response says what " +
				"was due and what actually came back.",
			ZH: "按成员退出 QQ 群的方式结束该账户，并收回其邀请获得的重置卡。" +
				"--mode=disable 保留账户（可恢复，QQ 号仍被占用）；--mode=delete 彻底删除账户及其全部数据。" +
				"已用掉的卡不会从别处补收——响应会分别给出应收回与实际收回的数量。",
		},
		Args: []console.Arg{
			{Name: "id|username", Hint: console.Text{EN: "account id or username", ZH: "账户 id 或用户名"}, Required: true},
		},
		Flags: []console.Flag{
			{Name: "--mode", Hint: console.Text{EN: "disable (default) or delete", ZH: "disable（默认）或 delete"}, Value: "MODE"},
			{Name: "--note", Hint: console.Text{EN: "why, for the audit log", ZH: "原因，记入审计日志"}, Value: "TEXT"},
		},
		Examples:    []string{"user depart alice --yes", "user depart alice --mode=delete --note 'left the group' -y"},
		SeeAlso:     []string{"invite departures"},
		Permission:  "users",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/users/{id}/departure"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			ref, err := console.RequireArg(rt, "account id or username")
			if err != nil {
				return err
			}
			uid, err := console.ResolveUser(rt, ref)
			if err != nil {
				return err
			}
			mode := rt.String("mode")
			if mode == "" {
				mode = ModeDisable
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/users/"+url.PathEscape(uid)+"/departure",
				map[string]any{"mode": mode, "note": rt.String("note")})
			if err != nil {
				return err
			}
			departure := object(object(data)["departure"])
			if rt.Session.Lang == "zh" {
				rt.Printf("退群处理完成（%s）。应收回 %s 张重置卡，实际收回 %s 张。\n",
					text(departure["mode"]), text(departure["cards_due"]), text(departure["cards_revoked"]))
			} else {
				rt.Printf("departure processed (%s). %s card(s) due, %s taken back.\n",
					text(departure["mode"]), text(departure["cards_due"]), text(departure["cards_revoked"]))
			}
			return nil
		},
	})

	console.Register(console.Command{
		Name:    "invite departures",
		Group:   "invites",
		Summary: console.Text{EN: "List processed group departures", ZH: "列出已处理的退群记录"},
		Usage:   "invite departures [--limit N] [--offset N]",
		Flags: []console.Flag{
			{Name: "--limit", Hint: console.Text{EN: "page size, default 50", ZH: "每页数量，默认 50"}, Value: "N"},
			{Name: "--offset", Hint: console.Text{EN: "rows to skip", ZH: "跳过的行数"}, Value: "N"},
		},
		Examples:   []string{"invite departures", "invite departures --json"},
		SeeAlso:    []string{"user depart", "invite stats"},
		Permission: "invites",
		Endpoints:  []string{"GET /api/admin/departures"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			q := url.Values{}
			q.Set("limit", strconv.Itoa(rt.IntOr("limit", 50)))
			if v := rt.IntOr("offset", 0); v != 0 {
				q.Set("offset", strconv.Itoa(v))
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/departures?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			list, _ := object(data)["departures"].([]any)
			rows := make([][]string, 0, len(list))
			for _, raw := range list {
				d := object(raw)
				inviter := text(d["inviter_name"])
				if inviter == "" {
					// A deleted inviter has no name left to show; the id is
					// what the audit trail keeps.
					inviter = text(d["inviter_id"])
				}
				rows = append(rows, []string{
					text(d["username"]), text(d["qq"]), inviter, text(d["mode"]),
					text(d["reward_cards_due"]) + "/" + text(d["cards_revoked"]),
					text(d["source"]), when(d["created_at"]),
				})
			}
			return rt.Table([]string{"username", "qq", "inviter", "mode", "due/revoked", "source", "at"}, rows)
		},
	})
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func text(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return fmt.Sprint(value)
	}
}

func when(v any) string {
	ms, err := strconv.ParseFloat(text(v), 64)
	if err != nil || ms <= 0 {
		return "-"
	}
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02 15:04")
}
