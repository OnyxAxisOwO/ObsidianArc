package console

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func init() {
	registerCommand(Command{
		Examples: []string{"log content-settings", "log content-settings --enabled true"},
		Name:     "log content-settings", Group: "operations", Summary: Text{EN: "Read or change API content recording", ZH: "查看或切换 API 正文记录"},
		Usage: "log content-settings [--enabled BOOL]", Permission: "logs",
		Flags:     []Flag{{Name: "--enabled", Hint: Text{EN: "Enable recording", ZH: "开启正文记录"}, Value: "BOOL"}},
		Endpoints: []string{"GET /api/admin/logs/content-settings", "PUT /api/admin/logs/content-settings"},
		Run: func(_ context.Context, rt *Runtime) error {
			method := http.MethodGet
			var body any
			if rt.Present("enabled") {
				method = http.MethodPut
				body = map[string]any{"enabled": rt.Bool("enabled")}
			}
			data, _, err := rt.Call(method, "/api/admin/logs/content-settings", body)
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"enabled", strconv.FormatBool(asBoolVal(asMap(data)["enabled"]))}})
		},
	})
	registerCommand(Command{
		Examples: []string{"log content 01ARZ3NDEKTSV4RRFFQ69G5FAV", "log content 01ARZ3NDEKTSV4RRFFQ69G5FAV --json"},
		Name:     "log content", Group: "operations", Summary: Text{EN: "Read a recorded API request body", ZH: "查看 API 请求正文"},
		Usage: "log content ID", Permission: "logs", Args: []Arg{{Name: "id", Required: true}},
		Endpoints: []string{"GET /api/admin/logs/{id}/content"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/logs/"+url.PathEscape(rt.Arg(0))+"/content", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			return rt.Fields([][2]string{{"body", asStr(m["body"])}, {"truncated", strconv.FormatBool(asBoolVal(m["truncated"]))}})
		},
	})
	registerCommand(Command{
		Examples: []string{"log content-prune --days 7 --yes", "log content-prune --days 0 --yes"},
		Name:     "log content-prune", Group: "operations", Summary: Text{EN: "Clear recorded request bodies", ZH: "清理已记录请求正文"},
		Usage: "log content-prune --days N --yes", Permission: "logs", Destructive: true,
		Flags:     []Flag{{Name: "--days", Hint: Text{EN: "0 clears all", ZH: "0 清理全部"}, Value: "N"}},
		Endpoints: []string{"POST /api/admin/logs/content/prune"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !rt.Present("days") {
				return rt.Fail(Text{EN: "--days is required", ZH: "需要 --days"})
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/logs/content/prune", map[string]any{"days": rt.Int("days")})
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"removed", formatNumber(asNum(asMap(data)["removed"]))}})
		},
	})
}
