package console

import (
	"context"
	"net/http"
	"strings"
)

func init() {
	registerCommand(Command{
		Name:    "update status",
		Group:   "instance",
		Summary: Text{EN: "Show whether a newer release exists", ZH: "查看是否有更新的版本"},
		Usage:   "update status",
		Help: Text{
			EN: "Asks the release feed at most twice a day, and not at all while the update check is off in system settings. Notices are problems with this instance that need an administrator's attention.",
			ZH: "最多每天向发布源查询两次；系统设置中关闭「检查新版本」后不再查询。通知列出需要管理员处理的实例问题。",
		},
		Examples:   []string{"update status", "update status --json"},
		SeeAlso:    []string{"setting get", "setting set"},
		Permission: "super_admin",
		Endpoints:  []string{"GET /api/admin/update"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/update", nil)
			if err != nil {
				return err
			}
			state := asMap(data)
			var kinds []string
			for _, notice := range asSlice(state["notices"]) {
				kinds = append(kinds, asStr(asMap(notice)["kind"]))
			}
			return rt.Fields([][2]string{
				{"current", asStr(state["current"])},
				{"latest", asStr(state["latest"])},
				{"update_available", yesNo(asBoolVal(state["update_available"]))},
				{"published_at", asStr(state["published_at"])},
				{"url", asStr(state["url"])},
				{"check_disabled", yesNo(asBoolVal(state["check_disabled"]))},
				{"notices", strings.Join(kinds, ", ")},
			})
		},
	})
}
