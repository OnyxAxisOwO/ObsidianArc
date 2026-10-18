package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
)

// ---------------------------------------------------------------------
// pref list | set | wallpaper-clear
//
// Permission is Anyone throughout: every command here calls a route
// mounted behind auth.RequireUser, not auth.RequireAdmin, and every one of
// them reads MustUser(r.Context()) for whose row to touch — there is no
// id or username argument anywhere in this file for that reason. A caller
// cannot even spell another account's preferences, which is what makes
// Anyone safe to grant to every signed-in session rather than to a "users"
// grant the way the accounts family needs.
//
// The wallpaper *image* has no command: PUT /api/preferences/wallpaper
// takes base64 image bytes, which is a file-upload payload, not something
// a command line has any business typing in. A command that cannot do its
// job is worse than no command, so only the read side (folded into
// `pref list`, the field lives in the same document) and the delete side
// exist here.
// ---------------------------------------------------------------------

func init() {
	registerCommand(Command{
		Name:    "pref list",
		Group:   "preferences",
		Summary: Text{EN: "Show your interface preferences", ZH: "显示你的界面偏好设置"},
		Usage:   "pref list",
		Help: Text{
			EN: "Preferences are one JSON document per account (theme, accent, default model and the " +
				"rest) that follows you between devices. A field never saved is simply absent here, " +
				"which means the interface's own built-in default is in effect for it.",
			ZH: "偏好设置是每个账户一份 JSON 文档（主题、强调色、默认模型等），会跟随账户在不同设备间同步。" +
				"从未保存过的字段不会出现在这里，此时生效的是界面自身的默认值。",
		},
		Examples:   []string{"pref list", "pref list --json"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/preferences"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/preferences", nil)
			if err != nil {
				return err
			}
			return renderPreferences(rt, data)
		},
	})

	registerCommand(Command{
		Name:  "pref set",
		Group: "preferences",
		Summary: Text{
			EN: "Change one or more of your interface preferences",
			ZH: "修改一项或多项界面偏好设置",
		},
		Usage: "pref set [flags]",
		Help: Text{
			EN: "Only the flags you give are changed — an absent flag leaves that preference exactly " +
				"as it was, the same contract user edit uses for an account. --default-model-id '' " +
				"is a real choice, not a no-op: it means whichever model you can reach first, which " +
				"is also what a new account starts with.",
			ZH: "只会修改你给出的选项，未给出的字段保持原样，与 user edit 对账户字段的约定一致。" +
				"--default-model-id '' 是一个真实的选择而非什么都不做：表示使用你能访问的第一个模型，" +
				"这也是新账户的初始状态。",
		},
		Flags: []Flag{
			{Name: "--theme", Hint: Text{EN: "light, dark or auto", ZH: "light、dark 或 auto"}, Value: "MODE"},
			{Name: "--accent", Hint: Text{
				EN: "violet, neutral, red, pink, indigo, blue, cyan, teal, green, orange or custom",
				ZH: "violet、neutral、red、pink、indigo、blue、cyan、teal、green、orange 或 custom",
			}, Value: "NAME"},
			{Name: "--custom-accent", Hint: Text{EN: "hex colour, used when --accent is custom", ZH: "十六进制颜色，仅当 --accent 为 custom 时生效"}, Value: "HEX"},
			{Name: "--default-model-id", Hint: Text{EN: "model id for a new chat, or '' for the first available", ZH: "新建对话使用的模型 id，留空表示第一个可用模型"}, Value: "ID"},
			{Name: "--reasoning-enabled", Hint: Text{EN: "turn extended reasoning on or off by default", ZH: "默认开启或关闭扩展推理"}, Value: "BOOL"},
			{Name: "--reasoning-effort", Hint: Text{EN: "low, medium or high", ZH: "low、medium 或 high"}, Value: "EFFORT"},
			{Name: "--language", Hint: Text{EN: "en or zh", ZH: "en 或 zh"}, Value: "LANG"},
			{Name: "--send-on-enter", Hint: Text{EN: "Enter sends the message instead of adding a line break", ZH: "回车发送消息，而不是换行"}, Value: "BOOL"},
			{Name: "--rail-collapsed", Hint: Text{EN: "start the side rail collapsed", ZH: "默认收起侧边栏"}, Value: "BOOL"},
			{Name: "--auto-use-reset-card", Hint: Text{
				EN: "when an allowance is exhausted, spend the reset card that expires first and continue automatically",
				ZH: "额度用尽时，自动使用最早到期的重置卡并继续本次请求",
			}, Value: "BOOL"},
		},
		Examples: []string{
			"pref set --theme dark --accent violet",
			"pref set --reasoning-effort high --send-on-enter=false",
		},
		Permission: Anyone,
		Endpoints:  []string{"PATCH /api/preferences"},
		Run: func(_ context.Context, rt *Runtime) error {
			body := bodyBuilder{}
			body.str(rt, "theme", "theme")
			body.str(rt, "accent", "accent")
			body.str(rt, "custom-accent", "custom_accent")
			body.str(rt, "default-model-id", "default_model_id")
			body.boolv(rt, "reasoning-enabled", "reasoning_enabled")
			body.str(rt, "reasoning-effort", "reasoning_effort")
			body.str(rt, "language", "language")
			body.boolv(rt, "send-on-enter", "send_on_enter")
			body.boolv(rt, "rail-collapsed", "rail_collapsed")
			body.boolv(rt, "auto-use-reset-card", "auto_use_reset_card")

			if len(body) == 0 {
				return rt.Fail(Text{EN: "nothing to change: give at least one flag", ZH: "没有需要修改的内容：请至少给出一个选项"})
			}

			data, _, err := rt.Call(http.MethodPatch, "/api/preferences", map[string]any(body))
			if err != nil {
				return err
			}
			return renderPreferences(rt, data)
		},
	})

	registerCommand(Command{
		Name:    "pref wallpaper-clear",
		Group:   "preferences",
		Summary: Text{EN: "Remove your wallpaper", ZH: "移除你的壁纸"},
		Usage:   "pref wallpaper-clear --yes",
		Help: Text{
			EN: "Deletes the stored wallpaper image and returns the chat background to the plain " +
				"theme surface. Setting a new wallpaper stays in the settings interface — it is image " +
				"bytes, which a command line has no way to carry.",
			ZH: "删除已保存的壁纸图片，聊天背景恢复为主题本身的纯色表面。设置新壁纸仍需在设置界面完成——" +
				"那是图片数据，命令行无法承载。",
		},
		Examples:    []string{"pref wallpaper-clear --yes", "pref wallpaper-clear -y"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/preferences/wallpaper"},
		Run: func(_ context.Context, rt *Runtime) error {
			if _, _, err := rt.Call(http.MethodDelete, "/api/preferences/wallpaper", nil); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "deleted.", ZH: "已删除。"})
		},
	})
}

// renderPreferences is shared by `pref list` and `pref set`: both end with
// the same document (Get's answer and Merge's answer have the same shape,
// "preferences": {...}) and both want the same table-mode rendering of it.
// --json bypasses this entirely — Fields prints the server's own response
// body the moment rt.rawJSON is set, which Call already did.
func renderPreferences(rt *Runtime, data any) error {
	prefs := asMap(asMap(data)["preferences"])

	// A field this binary knows nothing about (a newer interface release
	// saved it, or nothing was ever saved at all) still has to render
	// rather than vanish or panic — the document is deliberately arbitrary
	// JSON server-side (internal/user/preferences.go), so table mode reads
	// it by iterating the map rather than by naming each field twice.
	if len(prefs) == 0 && !rt.effectiveJSON() {
		return rt.confirm(Text{EN: "no preferences saved yet; interface defaults are in effect.", ZH: "尚未保存任何偏好设置，当前均为界面默认值。"})
	}

	keys := make([]string, 0, len(prefs))
	for k := range prefs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([][2]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, [2]string{k, formatPrefValue(prefs[k])})
	}
	return rt.Fields(pairs)
}

// formatPrefValue renders one stored preference value. Most of the document
// is flat scalars, but wallpaper is a nested object (url, dim, blur, …) —
// re-encoding whatever does not fit the scalar cases keeps that row honest
// instead of printing "map[...]"'s Go-internal spelling.
func formatPrefValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "-"
	case bool:
		return yesNo(val)
	case string:
		if val == "" {
			return "-"
		}
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(encoded)
	}
}
