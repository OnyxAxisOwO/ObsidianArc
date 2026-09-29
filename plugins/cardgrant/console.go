package cardgrant

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
)

func init() {
	console.Register(console.Command{
		Plugin:  Name,
		Name:    "cardgrant grant",
		Group:   "cards",
		Summary: console.Text{EN: "Grant a usage reset card to all users", ZH: "向全体用户发放一张重置卡"},
		Usage:   "cardgrant grant [--name TEXT] [--window 5h|1w|1m|full] [--days N] --yes",
		Help: console.Text{
			EN: "Grants 1 reset card to every registered account on the instance, with custom name, " +
				"quota window (5h, 1w, 1m, or full), and expiry duration in days (default 7).",
			ZH: "向实例中的所有注册用户发放 1 张用量重置卡，可自定义名称、重置范围（5h、1w、1m 或 full）" +
				"以及有效天数（默认 7 天）。",
		},
		Flags: []console.Flag{
			{Name: "--name", Hint: console.Text{EN: "card display name", ZH: "卡片显示名称"}, Value: "TEXT"},
			{Name: "--window", Hint: console.Text{EN: "5h, 1w, 1m, or full (default 5h)", ZH: "5h、1w、1m 或 full（默认 5h）"}, Value: "WINDOW"},
			{Name: "--days", Hint: console.Text{EN: "validity in days (default 7)", ZH: "有效天数（默认 7）"}, Value: "N"},
		},
		Examples: []string{
			"cardgrant grant --name 'Qwen 系列模型下架补偿' --window 5h --days 7 --yes",
			"cardgrant grant --name '周末福利' --window full --days 3 -y",
		},
		Permission:  "users",
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/cardgrant/grant"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			name := rt.String("name")
			if name == "" {
				name = "用量重置卡"
			}
			window := rt.String("window")
			if window == "" {
				window = "5h"
			}
			days := rt.Int("days")
			if days <= 0 {
				days = 7
			}

			data, _, err := rt.Call(http.MethodPost, "/api/admin/cardgrant/grant", map[string]any{
				"name":   name,
				"window": window,
				"days":   days,
			})
			if err != nil {
				return err
			}

			granted := object(data)["granted"]
			if rt.Session.Lang == "zh" {
				rt.Printf("已成功向 %v 位用户发放【%s】（%s，有效期 %d 天）重置卡。\n",
					granted, name, window, days)
			} else {
				rt.Printf("Successfully granted \"%s\" (%s, valid for %d days) to %v users.\n",
					name, window, days, granted)
			}
			return nil
		},
	})

	console.Register(console.Command{
		Plugin:  Name,
		Name:    "cardgrant list",
		Group:   "cards",
		Summary: console.Text{EN: "List mass card grant history", ZH: "列出全员发卡记录"},
		Usage:   "cardgrant list [--limit N]",
		Flags: []console.Flag{
			{Name: "--limit", Hint: console.Text{EN: "max entries to show (default 20)", ZH: "显示条数（默认 20）"}, Value: "N"},
		},
		Permission: "users",
		Endpoints:  []string{"GET /api/admin/cardgrant/grants"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			limit := rt.Int("limit")
			if limit <= 0 {
				limit = 20
			}

			data, _, err := rt.Call(http.MethodGet, fmt.Sprintf("/api/admin/cardgrant/grants?limit=%d", limit), nil)
			if err != nil {
				return err
			}

			items := slice(object(data)["grants"])
			if len(items) == 0 {
				if rt.Session.Lang == "zh" {
					rt.Printf("暂无全员发卡记录。\n")
				} else {
					rt.Printf("No mass card grant records found.\n")
				}
				return nil
			}

			headers := []string{"TIME", "NAME", "WINDOW", "RECIPIENTS", "EXPIRES", "OPERATOR"}
			if rt.Session.Lang == "zh" {
				headers = []string{"发放时间", "卡片名称", "重置范围", "发放人数", "截止日期", "操作人"}
			}

			rows := make([][]string, 0, len(items))
			for _, item := range items {
				obj := object(item)
				created := when(obj["created_at"])
				expires := when(obj["expires_at"])
				win := strVal(obj["windows"])
				if win == "" {
					win = "full"
				}
				operator := strVal(obj["actor_username"])
				if operator == "" {
					operator = strVal(obj["actor_id"])
				}
				rows = append(rows, []string{
					created,
					strVal(obj["name"]),
					win,
					fmt.Sprintf("%v", obj["recipient_count"]),
					expires,
					operator,
				})
			}
			return rt.Table(headers, rows)
		},
	})
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func slice(v any) []any {
	s, _ := v.([]any)
	return s
}

func strVal(v any) string {
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
	ms, err := strconv.ParseFloat(strVal(v), 64)
	if err != nil || ms <= 0 {
		return "-"
	}
	return time.UnixMilli(int64(ms)).Format("2006-01-02 15:04")
}
