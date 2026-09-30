package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// The daily check-in: `checkin config` is the administrator's, the rest is the
// account's own. docs/architecture/bonus-and-checkin.md has the rules.

func rewardText(v any) string {
	r := asMap(v)
	switch asStr(r["kind"]) {
	case "bonus":
		return fmt.Sprintf("bonus %s credits (%s days)", numberString(r["amount"]), numberString(r["valid_days"]))
	case "card":
		return fmt.Sprintf("%s card(s), %s days", numberString(r["cards"]), numberString(r["valid_days"]))
	}
	return "-"
}

func init() {
	registerCommand(Command{
		Name:       "checkin config",
		Group:      "bonus",
		Summary:    Text{EN: "Show the check-in settings and rewards", ZH: "显示签到的设置与奖励"},
		Usage:      "checkin config",
		Examples:   []string{"checkin config", "checkin config --json"},
		SeeAlso:    []string{"checkin config set"},
		Permission: "usage",
		Endpoints:  []string{"GET /api/admin/checkin"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/checkin", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			if err := rt.Fields([][2]string{
				{"enabled", yesNo(asBoolVal(m["enabled"]))},
				{"timezone", asStr(m["timezone"])},
				{"daily reward", rewardText(m["daily"])},
			}); err != nil || rt.effectiveJSON() {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(m["rules"]) {
				r := asMap(raw)
				rows = append(rows, []string{asStr(r["id"]), asStr(r["title"]), asStr(r["basis"]), numberString(r["days"]), rewardText(r["reward"])})
			}
			return rt.Table([]string{"id", "title", "basis", "days", "reward"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "checkin config set",
		Group:   "bonus",
		Summary: Text{EN: "Change the check-in settings and rewards", ZH: "修改签到的设置与奖励"},
		Usage:   "checkin config set [--enabled B] [--timezone TZ] [--daily JSON] [--rules JSON]",
		Help: Text{
			EN: "--daily is one reward, --rules the whole list of milestones; both as JSON, replacing what is there. " +
				"A reward is {\"kind\":\"bonus\",\"bar_id\":…,\"amount\":…,\"valid_days\":…} or " +
				"{\"kind\":\"card\",\"name\":…,\"windows\":[\"5h\"],\"cards\":1,\"valid_days\":…}; a milestone is " +
				"{\"id\":…,\"title\":…,\"basis\":\"streak\"|\"month\",\"days\":N,\"reward\":…}.",
			ZH: "--daily 是一份奖励，--rules 是整张里程碑列表；都用 JSON，会替换现有的内容。奖励是 " +
				"{\"kind\":\"bonus\",\"bar_id\":…,\"amount\":…,\"valid_days\":…} 或 " +
				"{\"kind\":\"card\",\"name\":…,\"windows\":[\"5h\"],\"cards\":1,\"valid_days\":…}；里程碑是 " +
				"{\"id\":…,\"title\":…,\"basis\":\"streak\"（连续）|\"month\"（本月累计）,\"days\":N,\"reward\":…}。",
		},
		Flags: []Flag{
			{Name: "--enabled", Hint: Text{EN: "switch check-in on or off (true/false)", ZH: "开启或关闭签到（true/false）"}, Value: "BOOL"},
			{Name: "--timezone", Hint: Text{EN: "the time zone a day is counted in, e.g. Asia/Shanghai", ZH: "按哪个时区算一天，如 Asia/Shanghai"}, Value: "TZ"},
			{Name: "--daily", Hint: Text{EN: "the daily reward, JSON; {} for none", ZH: "每日奖励，JSON；{} 表示没有"}, Value: "JSON"},
			{Name: "--rules", Hint: Text{EN: "the milestones, a JSON list", ZH: "里程碑，JSON 列表"}, Value: "JSON"},
		},
		Examples: []string{
			"checkin config set --enabled true --timezone Asia/Shanghai",
			`checkin config set --rules '[{"id":"w1","title":"连签 7 天","basis":"streak","days":7,"reward":{"kind":"card","name":"周卡","windows":["5h"],"cards":1,"valid_days":30}}]'`,
		},
		Permission: "usage",
		Endpoints:  []string{"PUT /api/admin/checkin"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/checkin", nil)
			if err != nil {
				return err
			}
			body := map[string]any{}
			for k, v := range asMap(data) {
				body[k] = v
			}
			bb := bodyBuilder(body)
			bb.boolv(rt, "enabled", "enabled")
			bb.str(rt, "timezone", "timezone")
			for flag, key := range map[string]string{"daily": "daily", "rules": "rules"} {
				if !rt.Present(flag) {
					continue
				}
				var parsed any
				if err := json.Unmarshal([]byte(rt.String(flag)), &parsed); err != nil {
					return rt.Errorf("--%s is not JSON: %v", flag, err)
				}
				if m, ok := parsed.(map[string]any); ok && len(m) == 0 {
					parsed = map[string]any{"kind": ""}
				}
				body[key] = parsed
			}
			_, _, err = rt.Call(http.MethodPut, "/api/admin/checkin", body)
			return err
		},
	})

	registerCommand(Command{
		Name:       "checkin",
		Group:      "credit",
		Summary:    Text{EN: "Show your check-in progress", ZH: "显示你的签到进度"},
		Usage:      "checkin",
		Examples:   []string{"checkin", "checkin --json"},
		SeeAlso:    []string{"checkin now", "checkin claim"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/checkin"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/checkin", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			if !asBoolVal(m["enabled"]) {
				rt.Printf("check-in is not switched on\n")
				return nil
			}
			if err := rt.Fields([][2]string{
				{"today", asStr(m["today"])},
				{"checked in today", yesNo(asBoolVal(m["checked_in_today"]))},
				{"streak", numberString(m["streak"])},
				{"days this month", numberString(m["month_count"])},
				{"daily reward", rewardText(m["daily"])},
			}); err != nil || rt.effectiveJSON() {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(m["rules"]) {
				r := asMap(raw)
				state := "-"
				switch {
				case asBoolVal(r["claimed"]):
					state = "claimed"
				case asBoolVal(r["claimable"]):
					state = "claim it"
				}
				rows = append(rows, []string{asStr(r["id"]), asStr(r["title"]), numberString(r["progress"]) + "/" + numberString(r["days"]), rewardText(r["reward"]), state})
			}
			return rt.Table([]string{"id", "title", "progress", "reward", "state"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "checkin now",
		Group:      "credit",
		Summary:    Text{EN: "Check in for today", ZH: "今天签到"},
		Usage:      "checkin now",
		Examples:   []string{"checkin now", "checkin now --json"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/checkin"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodPost, "/api/checkin", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			rt.Printf("checked in %s; streak %s; reward: %s\n", asStr(m["day"]), numberString(m["streak"]), rewardText(m["reward"]))
			return nil
		},
	})

	registerCommand(Command{
		Name:       "checkin claim",
		Group:      "credit",
		Summary:    Text{EN: "Claim a milestone reward", ZH: "领取里程碑奖励"},
		Usage:      "checkin claim <milestone-id>",
		Args:       []Arg{{Name: "milestone-id", Hint: Text{EN: "from `checkin`", ZH: "来自 `checkin`"}, Required: true}},
		Examples:   []string{"checkin claim w1", "checkin claim m15 --json"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/checkin/claims/{rule}"},
		Run: func(_ context.Context, rt *Runtime) error {
			id := rt.Arg(0)
			if id == "" {
				return rt.Errorf("a milestone id is required")
			}
			data, _, err := rt.Call(http.MethodPost, "/api/checkin/claims/"+url.PathEscape(id), nil)
			if err != nil {
				return err
			}
			rt.Printf("claimed: %s\n", rewardText(asMap(data)["reward"]))
			return nil
		},
	})
}
