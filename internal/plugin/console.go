package plugin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
)

// The console's commands for the plugins screen. They live here, beside the
// routes, for the reason a plugin's own commands live with the plugin: the
// console's route-parity test reads admin.go, and these routes are mounted
// from this package.
func init() {
	view := PermissionView + "," + PermissionManage + "," + PermissionRemove
	nameArg := []console.Arg{{Name: "name", Hint: console.Text{EN: "plugin name", ZH: "插件名"}, Required: true}}
	codeFlag := console.Flag{Name: "--code", Hint: console.Text{
		EN: "a code from your authenticator, or a recovery code", ZH: "验证器中的验证码，或一个恢复码"},
		Value: "CODE", Sensitive: true}

	console.Register(console.Command{
		Name:    "plugin list",
		Group:   "instance",
		Summary: console.Text{EN: "List the plugins this build carries", ZH: "列出本构建带有的插件"},
		Usage:   "plugin list",
		Help: console.Text{
			EN: "Every plugin compiled into this binary and where it stands here: available (not installed), " +
				"enabled or disabled. A plugin installed by another build that this one lacks is listed as missing.",
			ZH: "列出编译进本程序的每个插件及其在本实例的状态：可安装（未安装）、已启用或已禁用。" +
				"由其他构建安装而本构建没有的插件显示为缺失。",
		},
		Examples:   []string{"plugin list", "plugin list --json"},
		SeeAlso:    []string{"plugin show", "plugin install"},
		Permission: view,
		Endpoints:  []string{"GET /api/admin/plugins"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/plugins", nil)
			if err != nil {
				return err
			}
			list, _ := object(data)["plugins"].([]any)
			rows := make([][]string, 0, len(list))
			for _, raw := range list {
				p := object(raw)
				manifest := object(p["manifest"])
				state := text(p["state"])
				if p["missing"] == true {
					state += " (missing)"
				}
				rows = append(rows, []string{text(p["name"]), text(manifest["version"]), state,
					localised(rt, manifest["title"])})
			}
			return rt.Table([]string{"name", "version", "state", "title"}, rows)
		},
	})

	console.Register(console.Command{
		Name:    "plugin show",
		Group:   "instance",
		Summary: console.Text{EN: "Show one plugin's manifest", ZH: "查看插件清单"},
		Usage:   "plugin show <name>",
		Help: console.Text{
			EN: "The plugin's manifest and what it attaches: settings, account fields, sign-in guards, routes, " +
				"console commands and migrations, and who installed it when.",
			ZH: "插件的清单及其接入的内容：设置项、账户字段、登录守卫、路由、控制台命令与迁移，以及安装人和安装时间。",
		},
		Args:       nameArg,
		Examples:   []string{"plugin show qqgroup", "plugin show riskcontrol --json"},
		SeeAlso:    []string{"plugin list"},
		Permission: view,
		Endpoints:  []string{"GET /api/admin/plugins/{name}"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			name, err := console.RequireArg(rt, "plugin name")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/plugins/"+url.PathEscape(name), nil)
			if err != nil {
				return err
			}
			p := object(object(data)["plugin"])
			manifest := object(p["manifest"])
			installed := object(p["installed"])
			c := object(p["contributions"])
			var settingKeys []string
			for _, raw := range list(c["settings"]) {
				settingKeys = append(settingKeys, text(object(raw)["key"]))
			}
			var routes []string
			for _, raw := range list(c["admin_routes"]) {
				routes = append(routes, text(object(raw)["pattern"]))
			}
			for _, raw := range list(c["public_routes"]) {
				routes = append(routes, text(raw))
			}
			return rt.Fields([][2]string{
				{"name", text(p["name"])},
				{"title", localised(rt, manifest["title"])},
				{"version", text(manifest["version"])},
				{"state", text(p["state"])},
				{"description", localised(rt, manifest["description"])},
				{"author", text(manifest["author"])},
				{"license", text(manifest["license"])},
				{"installed", when(installed["at"]) + " " + text(installed["by"])},
				{"settings", strings.Join(settingKeys, ", ")},
				{"fields", joined(c["fields"])},
				{"guards", joined(c["guards"])},
				{"routes", strings.Join(routes, ", ")},
				{"commands", joined(c["commands"])},
				{"migrations", joined(c["migrations"])},
			})
		},
	})

	console.Register(console.Command{
		Name:    "plugin install",
		Group:   "instance",
		Summary: console.Text{EN: "Install a plugin", ZH: "安装插件"},
		Usage:   "plugin install <name> [--off] [--code CODE]",
		Help: console.Text{
			EN: "Runs the plugin's migrations and records it as installed, switched on unless --off is given. " +
				"Its settings are configured afterwards, like any other.",
			ZH: "执行插件的迁移并记为已安装；除非给出 --off，安装后立即启用。插件的设置之后与其他设置一样配置。",
		},
		Args: nameArg,
		Flags: []console.Flag{
			codeFlag,
			{Name: "--off", Hint: console.Text{EN: "install it switched off", ZH: "安装后保持禁用"}},
		},
		Examples:   []string{"plugin install qqgroup", "plugin install riskcontrol --off"},
		SeeAlso:    []string{"plugin enable", "plugin uninstall"},
		Permission: PermissionManage,
		Endpoints:  []string{"POST /api/admin/plugins/{name}/install"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			return change(rt, "install", map[string]any{
				"enable":          !rt.Bool("off"),
				"two_factor_code": rt.String("code"),
			})
		},
	})

	console.Register(console.Command{
		Name:       "plugin enable",
		Group:      "instance",
		Summary:    console.Text{EN: "Switch an installed plugin on", ZH: "启用已安装的插件"},
		Usage:      "plugin enable <name>",
		Args:       nameArg,
		Examples:   []string{"plugin enable qqgroup", "plugin enable riskcontrol --json"},
		SeeAlso:    []string{"plugin disable"},
		Permission: PermissionManage,
		Endpoints:  []string{"POST /api/admin/plugins/{name}/enable"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			return change(rt, "enable", nil)
		},
	})

	console.Register(console.Command{
		Name:    "plugin disable",
		Group:   "instance",
		Summary: console.Text{EN: "Switch a plugin off", ZH: "禁用插件"},
		Usage:   "plugin disable <name> --code CODE",
		Help: console.Text{
			EN: "Keeps everything the plugin has and stops everything it does. Needs a two-step code.",
			ZH: "保留插件的全部数据，停止它的全部功能。需要两步验证码。",
		},
		Args:       nameArg,
		Flags:      []console.Flag{codeFlag},
		Examples:   []string{"plugin disable qqgroup --code 123456", "plugin disable riskcontrol --code 123456 --json"},
		SeeAlso:    []string{"plugin enable", "plugin uninstall"},
		Permission: PermissionManage,
		Endpoints:  []string{"POST /api/admin/plugins/{name}/disable"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			return change(rt, "disable", map[string]any{"two_factor_code": rt.String("code")})
		},
	})

	console.Register(console.Command{
		Name:    "plugin uninstall",
		Group:   "instance",
		Summary: console.Text{EN: "Uninstall a plugin", ZH: "卸载插件"},
		Usage:   "plugin uninstall <name> --code CODE [--purge] --yes",
		Help: console.Text{
			EN: "Takes the plugin off this instance. Its data stays for a later reinstall unless --purge is " +
				"given, which deletes its settings and drops its tables and columns. Needs a two-step code.",
			ZH: "从本实例移除插件。除非给出 --purge，其数据会保留以便日后重新安装；" +
				"--purge 会删除其设置并删除其数据表和字段。需要两步验证码。",
		},
		Args: nameArg,
		Flags: []console.Flag{
			codeFlag,
			{Name: "--purge", Hint: console.Text{EN: "delete its data too", ZH: "同时删除其数据"}},
		},
		Examples:    []string{"plugin uninstall qqgroup --code 123456 --yes", "plugin uninstall qqgroup --code 123456 --purge -y"},
		SeeAlso:     []string{"plugin disable", "plugin install"},
		Permission:  PermissionRemove,
		Destructive: true,
		Endpoints:   []string{"POST /api/admin/plugins/{name}/uninstall"},
		Run: func(_ context.Context, rt *console.Runtime) error {
			return change(rt, "uninstall", map[string]any{
				"two_factor_code": rt.String("code"), "purge": rt.Bool("purge"),
			})
		},
	})
}

func change(rt *console.Runtime, action string, body map[string]any) error {
	name, err := console.RequireArg(rt, "plugin name")
	if err != nil {
		return err
	}
	if body == nil {
		body = map[string]any{}
	}
	data, _, err := rt.Call(http.MethodPost, "/api/admin/plugins/"+url.PathEscape(name)+"/"+action, body)
	if err != nil {
		return err
	}
	p := object(object(data)["plugin"])
	if rt.Session.Lang == "zh" {
		rt.Printf("%s：%s\n", text(p["name"]), text(p["state"]))
	} else {
		rt.Printf("%s: %s\n", text(p["name"]), text(p["state"]))
	}
	return nil
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func text(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case float64:
		return fmt.Sprintf("%.0f", value)
	default:
		return fmt.Sprint(value)
	}
}

func joined(v any) string {
	var out []string
	for _, item := range list(v) {
		out = append(out, text(item))
	}
	return strings.Join(out, ", ")
}

func localised(rt *console.Runtime, v any) string {
	t := object(v)
	if rt.Session.Lang == "zh" && text(t["zh"]) != "" {
		return text(t["zh"])
	}
	return text(t["en"])
}

func when(v any) string {
	value, ok := v.(float64)
	if !ok || value <= 0 {
		return "-"
	}
	return time.UnixMilli(int64(value)).UTC().Format("2006-01-02 15:04")
}
