package console

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

func init() {
	registerCommand(Command{
		Name:       "backup status",
		Group:      "backup",
		Summary:    Text{EN: "Show instance backup settings and latest run", ZH: "查看实例备份设置和最近运行状态"},
		Usage:      "backup status",
		Help:       Text{EN: "Shows the S3-compatible destination, schedule, last successful upload, and any recent error. Storage credentials are never returned.", ZH: "显示 S3 兼容存储目标、计划、最近一次成功上传时间和错误。不会返回存储凭据。"},
		Examples:   []string{"backup status", "backup status --json"},
		SeeAlso:    []string{"backup configure", "backup test", "backup run"},
		Permission: "super_admin",
		Endpoints:  []string{"GET /api/admin/backup"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/backup", nil)
			if err != nil {
				return err
			}
			state := asMap(data)
			return rt.Fields([][2]string{
				{"enabled", yesNo(asBoolVal(state["enabled"]))},
				{"configured", yesNo(asBoolVal(state["configured"]))},
				{"endpoint", asStr(state["endpoint"])},
				{"bucket", asStr(state["bucket"])},
				{"region", asStr(state["region"])},
				{"prefix", asStr(state["prefix"])},
				{"interval_hours", fmt.Sprint(asNum(state["interval_hours"]))},
				{"retention_days", fmt.Sprint(asNum(state["retention_days"]))},
				{"running", yesNo(asBoolVal(state["running"]))},
				{"last_status", asStr(state["last_status"])},
				{"last_success_at", formatMS(state["last_success_at"])},
				{"next_run_at", formatMS(state["next_run_at"])},
				{"last_error", asStr(state["last_error"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "backup configure",
		Group:   "backup",
		Summary: Text{EN: "Update instance backup destination and schedule", ZH: "修改实例备份目标和计划"},
		Usage:   "backup configure [--enabled BOOL] [--endpoint URL] [--bucket NAME] [--region NAME] [--prefix PATH] [--interval-hours N] [--retention-days N]",
		Help: Text{
			EN: "Changes only the supplied fields. Existing access and secret keys are preserved. To add or rotate storage credentials, use the super-admin Backup page; accepting secrets on a command line would save them in terminal history.",
			ZH: "只修改给出的字段，并保留已保存的 Access Key 和 Secret Key。添加或更换凭据请使用超级管理员的“备份”页面；命令行输入密钥会将其留在终端历史中。",
		},
		Flags: []Flag{
			{Name: "--enabled", Hint: Text{EN: "true or false", ZH: "true 或 false"}, Value: "BOOL"},
			{Name: "--endpoint", Hint: Text{EN: "S3-compatible base URL", ZH: "S3 兼容存储的基础 URL"}, Value: "URL"},
			{Name: "--bucket", Hint: Text{EN: "bucket name", ZH: "存储桶名称"}, Value: "NAME"},
			{Name: "--region", Hint: Text{EN: "region; use auto for Cloudflare R2", ZH: "区域；Cloudflare R2 填 auto"}, Value: "NAME"},
			{Name: "--prefix", Hint: Text{EN: "relative object prefix", ZH: "相对对象前缀"}, Value: "PATH"},
			{Name: "--interval-hours", Hint: Text{EN: "1-168 hours", ZH: "1-168 小时"}, Value: "N"},
			{Name: "--retention-days", Hint: Text{EN: "1-3650 days", ZH: "1-3650 天"}, Value: "N"},
		},
		Examples:   []string{"backup configure --enabled true --interval-hours 24 --retention-days 7", "backup configure --region auto --prefix arc/prod"},
		SeeAlso:    []string{"backup status", "backup test"},
		Permission: "super_admin",
		Endpoints:  []string{"PUT /api/admin/backup"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() > 0 {
				return rt.Errorf("backup configure takes flags only")
			}
			if rt.Present("access-key-id") || rt.Present("secret-access-key") || rt.Present("secret-key") {
				return rt.Errorf("credentials can only be entered on the super-admin Backup page")
			}
			if !rt.Present("enabled") && !rt.Present("endpoint") && !rt.Present("bucket") && !rt.Present("region") &&
				!rt.Present("prefix") && !rt.Present("interval-hours") && !rt.Present("retention-days") {
				return rt.Errorf("supply at least one setting to change")
			}
			currentData, _, err := rt.Call(http.MethodGet, "/api/admin/backup", nil)
			if err != nil {
				return err
			}
			current := asMap(currentData)
			body := map[string]any{
				"enabled":           asBoolVal(current["enabled"]),
				"endpoint":          asStr(current["endpoint"]),
				"bucket":            asStr(current["bucket"]),
				"region":            asStr(current["region"]),
				"prefix":            asStr(current["prefix"]),
				"access_key_id":     "",
				"secret_access_key": "",
				"interval_hours":    int(asNum(current["interval_hours"])),
				"retention_days":    int(asNum(current["retention_days"])),
			}
			if rt.Present("enabled") {
				value, err := strconv.ParseBool(rt.String("enabled"))
				if err != nil {
					return rt.Errorf("--enabled must be true or false")
				}
				body["enabled"] = value
			}
			for flag, field := range map[string]string{
				"endpoint": "endpoint", "bucket": "bucket", "region": "region", "prefix": "prefix",
			} {
				if rt.Present(flag) {
					body[field] = rt.String(flag)
				}
			}
			for flag, field := range map[string]string{"interval-hours": "interval_hours", "retention-days": "retention_days"} {
				if rt.Present(flag) {
					value, err := strconv.Atoi(rt.String(flag))
					if err != nil {
						return rt.Errorf("--%s must be a whole number", flag)
					}
					body[field] = value
				}
			}
			if _, _, err := rt.Call(http.MethodPut, "/api/admin/backup", body); err != nil {
				return err
			}
			updated, _, err := rt.Call(http.MethodGet, "/api/admin/backup", nil)
			if err != nil {
				return err
			}
			state := asMap(updated)
			return rt.Fields([][2]string{
				{"enabled", yesNo(asBoolVal(state["enabled"]))},
				{"endpoint", asStr(state["endpoint"])},
				{"bucket", asStr(state["bucket"])},
				{"region", asStr(state["region"])},
				{"prefix", asStr(state["prefix"])},
				{"interval_hours", fmt.Sprint(asNum(state["interval_hours"]))},
				{"retention_days", fmt.Sprint(asNum(state["retention_days"]))},
			})
		},
	})

	registerCommand(Command{
		Name:       "backup test",
		Group:      "backup",
		Summary:    Text{EN: "Test instance backup storage permissions", ZH: "测试实例备份存储权限"},
		Usage:      "backup test",
		Help:       Text{EN: "Uploads a temporary probe object under this instance's owned prefix, checks that it is listed, then deletes it. The credentials need PutObject, ListBucket and DeleteObject access for the selected bucket and prefix.", ZH: "在本站专属前缀下上传临时探测对象，确认可以列出后将其删除。凭据需要对所选存储桶和前缀具有 PutObject、ListBucket 与 DeleteObject 权限。"},
		Examples:   []string{"backup test", "backup status"},
		SeeAlso:    []string{"backup configure", "backup run"},
		Permission: "super_admin",
		Endpoints:  []string{"POST /api/admin/backup/test"},
		Run: func(_ context.Context, rt *Runtime) error {
			if _, _, err := rt.Call(http.MethodPost, "/api/admin/backup/test", map[string]any{}); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("存储连接测试成功。\n")
			} else {
				rt.Printf("backup storage test passed.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:       "backup run",
		Group:      "backup",
		Summary:    Text{EN: "Run a full instance backup now", ZH: "立即运行完整实例备份"},
		Usage:      "backup run",
		Help:       Text{EN: "Starts one full database and attachment backup. It runs in the background, uses the same persisted lease as the automatic schedule, and may take several minutes.", ZH: "立即启动一次完整数据库与附件备份。任务在后台运行，与自动计划共用持久化租约，可能需要数分钟。"},
		Examples:   []string{"backup run", "backup status"},
		SeeAlso:    []string{"backup status", "backup test"},
		Permission: "super_admin",
		Endpoints:  []string{"POST /api/admin/backup/run"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodPost, "/api/admin/backup/run", map[string]any{})
			if err != nil {
				return err
			}
			state := asMap(data)
			return rt.Fields([][2]string{{"running", yesNo(asBoolVal(state["running"]))}})
		},
	})
}
