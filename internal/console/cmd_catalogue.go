package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// parseHeaders turns "X-Foo=bar,X-Bar=baz" into the map[string]string
// POST/PATCH /api/admin/providers takes for headers.
func parseHeaders(raw string) map[string]string {
	out := map[string]string{}
	for _, entry := range splitCSV(raw) {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// parseTiers turns "id:name:budget,id2:name2:budget2" into the
// []{id,name,budget} shape POST/PATCH /api/admin/models takes for
// reasoning_tiers.
func parseTiers(raw string) []map[string]any {
	var out []map[string]any
	for _, entry := range splitCSV(raw) {
		parts := strings.SplitN(entry, ":", 3)
		tier := map[string]any{}
		if len(parts) > 0 {
			tier["id"] = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			tier["name"] = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			var budget int
			fmt.Sscanf(strings.TrimSpace(parts[2]), "%d", &budget)
			tier["budget"] = budget
		}
		out = append(out, tier)
	}
	return out
}

func init() {
	registerCommand(Command{
		Name:       "provider list",
		Group:      "catalogue",
		Summary:    Text{EN: "List providers", ZH: "列出服务商"},
		Usage:      "provider list",
		Examples:   []string{"provider list", "provider list --json"},
		Permission: "providers",
		Endpoints:  []string{"GET /api/admin/providers"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/providers", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["providers"]) {
				p := asMap(raw)
				rows = append(rows, []string{
					asStr(p["id"]), asStr(p["name"]), asStr(p["kind"]), asStr(p["base_url"]),
					yesNo(asBoolVal(p["enabled"])), fmt.Sprint(asNum(p["model_count"])), asStr(p["api_key_hint"]),
				})
			}
			return rt.Table([]string{"id", "name", "kind", "base_url", "enabled", "models", "key"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "provider show",
		Group:      "catalogue",
		Summary:    Text{EN: "Show one provider and its models", ZH: "显示单个服务商及其模型"},
		Usage:      "provider show <id|name>",
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "provider id or name", ZH: "服务商 id 或名称"}, Required: true}},
		Examples:   []string{"provider show OpenAI", "provider show 01H8X…"},
		Permission: "providers",
		Endpoints:  []string{"GET /api/admin/providers"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "provider id or name")
			if err != nil {
				return err
			}
			pid, err := resolveProviderRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/providers", nil)
			if err != nil {
				return err
			}
			var p map[string]any
			for _, raw := range asSlice(asMap(data)["providers"]) {
				if m := asMap(raw); asStr(m["id"]) == pid {
					p = m
					break
				}
			}
			if p == nil {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有这个服务商。")
				}
				return rt.Errorf("no such provider.")
			}
			jsonMode := rt.effectiveJSON()
			var headers []string
			for k, v := range asMap(p["headers"]) {
				headers = append(headers, k+"="+asStr(v))
			}
			if err := rt.Fields([][2]string{
				{"id", asStr(p["id"])}, {"name", asStr(p["name"])}, {"kind", asStr(p["kind"])},
				{"base_url", asStr(p["base_url"])}, {"allow_insecure", yesNo(asBoolVal(p["allow_insecure"]))},
				{"api_key_hint", asStr(p["api_key_hint"])}, {"headers", strings.Join(headers, ", ")},
				{"reasoning_style", asStr(p["reasoning_style"])}, {"timeout_seconds", fmt.Sprint(asNum(p["timeout_seconds"]))},
				{"enabled", yesNo(asBoolVal(p["enabled"]))}, {"sort_order", fmt.Sprint(asNum(p["sort_order"]))},
				{"model_count", fmt.Sprint(asNum(p["model_count"]))}, {"created_at", formatMS(p["created_at"])},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			fmt.Fprintln(rt.Out, "\nmodels:")
			modelsData, _, err := rt.Call(http.MethodGet, "/api/admin/models?"+url.Values{"provider_id": {pid}}.Encode(), nil)
			if err != nil {
				return err
			}
			var mrows [][]string
			for _, raw := range asSlice(asMap(modelsData)["models"]) {
				m := asMap(raw)
				mrows = append(mrows, []string{asStr(m["id"]), asStr(m["display_name"]), asStr(m["model_id"]), yesNo(asBoolVal(m["enabled"]))})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"id", "display_name", "model_id", "enabled"}, mrows)
			return nil
		},
	})

	registerCommand(Command{
		Name:    "provider create",
		Group:   "catalogue",
		Summary: Text{EN: "Add a provider", ZH: "新增服务商"},
		Usage:   "provider create --name NAME --kind KIND --base-url URL (--api-key KEY | --copy-key-from REF) [flags]",
		Help: Text{
			EN: "Give exactly one of --api-key or --copy-key-from (another provider's stored key, " +
				"copied without ever being unsealed).",
			ZH: "请只给出 --api-key 或 --copy-key-from 之一（复制另一个服务商已保存的密钥，" +
				"全程不会被解封显示）。",
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-60 characters, required", ZH: "1-60 字符，必填"}, Value: "NAME"},
			{Name: "--kind", Hint: Text{EN: "openai or anthropic, required", ZH: "openai 或 anthropic，必填"}, Value: "KIND"},
			{Name: "--base-url", Hint: Text{EN: "must be https unless --allow-insecure and loopback", ZH: "必须为 https，除非 --allow-insecure 且为本机地址"}, Value: "URL"},
			{Name: "--allow-insecure", Hint: Text{EN: "permit plain http to a non-loopback host", ZH: "允许对非本机地址使用明文 http"}},
			{Name: "--api-key", Hint: Text{EN: "the credential, write-only", ZH: "凭据，仅写入不可再读出"}, Value: "KEY", Sensitive: true},
			{Name: "--copy-key-from", Hint: Text{EN: "another provider's ref, copies its sealed key", ZH: "另一个服务商的引用，复制其已封存密钥"}, Value: "REF"},
			{Name: "--headers", Hint: Text{EN: "comma list of Header=value, ≤20", ZH: "逗号分隔的 Header=value，最多 20 个"}, Value: "LIST", Sensitive: true},
			{Name: "--anthropic-version", Hint: Text{EN: "the anthropic-version header value", ZH: "anthropic-version 请求头的值"}, Value: "TEXT"},
			{Name: "--reasoning-style", Hint: Text{EN: "auto, none, anthropic, openai_effort, openrouter or qwen", ZH: "auto、none、anthropic、openai_effort、openrouter 或 qwen"}, Value: "STYLE"},
			{Name: "--timeout-seconds", Hint: Text{EN: "default 120, max 900", ZH: "默认 120，最大 900"}, Value: "N", Default: "120"},
			{Name: "--enabled", Hint: Text{EN: "default true", ZH: "默认 true"}, Value: "BOOL", Default: "true"},
			{Name: "--sort-order", Hint: Text{EN: "lower sorts first", ZH: "数值越小越靠前"}, Value: "N"},
		},
		Examples: []string{
			"provider create --name OpenAI --kind openai --base-url https://api.openai.com --api-key sk-…",
			"provider create --name OpenAI-EU --kind openai --base-url https://eu.api.openai.com --copy-key-from OpenAI",
		},
		Permission: "providers",
		Endpoints:  []string{"POST /api/admin/providers"},
		Run: func(_ context.Context, rt *Runtime) error {
			for _, req := range []string{"name", "kind", "base-url"} {
				if !rt.Present(req) || rt.String(req) == "" {
					if rt.Session.Lang == "zh" {
						return rt.Errorf("需要 --%s", req)
					}
					return rt.Errorf("--%s is required", req)
				}
			}
			body := bodyBuilder{
				"name":     rt.String("name"),
				"kind":     rt.String("kind"),
				"base_url": rt.String("base-url"),
			}
			body.boolv(rt, "allow-insecure", "allow_insecure")
			if rt.Present("api-key") {
				body["api_key"] = rt.String("api-key")
			} else if rt.Present("copy-key-from") {
				pid, err := resolveProviderRef(rt, rt.String("copy-key-from"))
				if err != nil {
					return err
				}
				body["copy_key_from"] = pid
			}
			if rt.Present("headers") {
				body["headers"] = parseHeaders(rt.String("headers"))
			}
			body.str(rt, "anthropic-version", "anthropic_version")
			body.str(rt, "reasoning-style", "reasoning_style")
			body.intv(rt, "timeout-seconds", "timeout_seconds")
			body.boolv(rt, "enabled", "enabled")
			body.intv(rt, "sort-order", "sort_order")

			data, _, err := rt.Call(http.MethodPost, "/api/admin/providers", map[string]any(body))
			if err != nil {
				return err
			}
			p := asMap(asMap(data)["provider"])
			return rt.Fields([][2]string{{"id", asStr(p["id"])}, {"name", asStr(p["name"])}, {"created_at", formatMS(p["created_at"])}})
		},
	})

	registerCommand(Command{
		Name:    "provider edit",
		Group:   "catalogue",
		Summary: Text{EN: "Edit a provider", ZH: "编辑服务商"},
		Usage:   "provider edit <id|name> [flags]",
		Help: Text{
			EN: "Only the flags you give are changed. An absent --api-key leaves the stored credential " +
				"untouched.",
			ZH: "只会修改你给出的选项。不给出 --api-key 时，已保存的凭据保持不变。",
		},
		Args: []Arg{{Name: "id|name", Hint: Text{EN: "provider id or name", ZH: "服务商 id 或名称"}, Required: true}},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-60 characters", ZH: "1-60 字符"}, Value: "NAME"},
			{Name: "--kind", Hint: Text{EN: "openai or anthropic", ZH: "openai 或 anthropic"}, Value: "KIND"},
			{Name: "--base-url", Hint: Text{EN: "https unless --allow-insecure and loopback", ZH: "https，除非 --allow-insecure 且为本机地址"}, Value: "URL"},
			{Name: "--allow-insecure", Hint: Text{EN: "permit plain http to a non-loopback host", ZH: "允许对非本机地址使用明文 http"}, Value: "BOOL"},
			{Name: "--api-key", Hint: Text{EN: "replace the stored credential", ZH: "替换已保存的凭据"}, Value: "KEY", Sensitive: true},
			{Name: "--headers", Hint: Text{EN: "comma list of Header=value, replaces the set", ZH: "逗号分隔的 Header=value，整体替换"}, Value: "LIST", Sensitive: true},
			{Name: "--anthropic-version", Hint: Text{EN: "the anthropic-version header value", ZH: "anthropic-version 请求头的值"}, Value: "TEXT"},
			{Name: "--reasoning-style", Hint: Text{EN: "auto, none, anthropic, openai_effort, openrouter or qwen", ZH: "auto、none、anthropic、openai_effort、openrouter 或 qwen"}, Value: "STYLE"},
			{Name: "--timeout-seconds", Hint: Text{EN: "max 900", ZH: "最大 900"}, Value: "N"},
			{Name: "--enabled", Hint: Text{EN: "true or false", ZH: "true 或 false"}, Value: "BOOL"},
			{Name: "--sort-order", Hint: Text{EN: "lower sorts first", ZH: "数值越小越靠前"}, Value: "N"},
		},
		Examples:   []string{"provider edit OpenAI --enabled false", "provider edit OpenAI --timeout-seconds 60"},
		Permission: "providers",
		Endpoints:  []string{"PATCH /api/admin/providers/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "provider id or name")
			if err != nil {
				return err
			}
			pid, err := resolveProviderRef(rt, ref)
			if err != nil {
				return err
			}
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			body.str(rt, "kind", "kind")
			body.str(rt, "base-url", "base_url")
			body.boolv(rt, "allow-insecure", "allow_insecure")
			body.str(rt, "api-key", "api_key")
			if rt.Present("headers") {
				body["headers"] = parseHeaders(rt.String("headers"))
			}
			body.str(rt, "anthropic-version", "anthropic_version")
			body.str(rt, "reasoning-style", "reasoning_style")
			body.intv(rt, "timeout-seconds", "timeout_seconds")
			body.boolv(rt, "enabled", "enabled")
			body.intv(rt, "sort-order", "sort_order")

			if len(body) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有需要修改的内容：请至少给出一个选项")
				}
				return rt.Errorf("nothing to change: give at least one flag")
			}
			data, _, err := rt.Call(http.MethodPatch, "/api/admin/providers/"+url.PathEscape(pid), map[string]any(body))
			if err != nil {
				return err
			}
			p := asMap(asMap(data)["provider"])
			return rt.Fields([][2]string{{"id", asStr(p["id"])}, {"name", asStr(p["name"])}, {"updated_at", formatMS(p["updated_at"])}})
		},
	})

	registerCommand(Command{
		Name:    "provider delete",
		Group:   "catalogue",
		Summary: Text{EN: "Delete a provider and every model it has", ZH: "删除服务商及其全部模型"},
		Usage:   "provider delete <id|name> --yes",
		Help: Text{
			EN: "This cascades: every model of this provider is deleted with it, and so are the group " +
				"grants pointing at those models. There is no server-side confirmation beyond --yes.",
			ZH: "这会级联删除：该服务商的全部模型会一并删除，指向这些模型的分组授权也会一并删除。" +
				"服务器不会再做二次确认，全部依赖 --yes。",
		},
		Args:        []Arg{{Name: "id|name", Hint: Text{EN: "provider id or name", ZH: "服务商 id 或名称"}, Required: true}},
		Examples:    []string{"provider delete OldProvider --yes", "provider delete OldProvider -y"},
		Permission:  "providers",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/providers/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "provider id or name")
			if err != nil {
				return err
			}
			pid, err := resolveProviderRef(rt, ref)
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/providers/"+url.PathEscape(pid), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已删除。\n")
			} else {
				rt.Printf("deleted.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "provider detect",
		Group:   "catalogue",
		Summary: Text{EN: "List the upstream models a provider offers", ZH: "列出服务商上游提供的模型"},
		Usage:   "provider detect <id|name>",
		Help: Text{
			EN: "Makes a live outbound call to the provider, so it is slow and can fail with the " +
				"upstream's own error. `configured` marks the ones already in the local catalogue.",
			ZH: "会向服务商发起一次真实的出站请求，因此较慢，也可能返回上游自身的错误。" +
				"configured 标记的是本地已配置的模型。",
		},
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "provider id or name", ZH: "服务商 id 或名称"}, Required: true}},
		Examples:   []string{"provider detect OpenAI", "provider detect OpenAI --json"},
		Permission: "providers,models",
		Endpoints:  []string{"POST /api/admin/providers/{id}/detect"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "provider id or name")
			if err != nil {
				return err
			}
			pid, err := resolveProviderRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/providers/"+url.PathEscape(pid)+"/detect", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["models"]) {
				m := asMap(raw)
				rows = append(rows, []string{asStr(m["model_id"]), asStr(m["display_name"]), yesNo(asBoolVal(m["configured"]))})
			}
			return rt.Table([]string{"model_id", "display_name", "configured"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "model list",
		Group:      "catalogue",
		Summary:    Text{EN: "List models", ZH: "列出模型"},
		Usage:      "model list [--provider REF]",
		Flags:      []Flag{{Name: "--provider", Hint: Text{EN: "filter to one provider, by ref", ZH: "按服务商筛选，可用引用"}, Value: "REF"}},
		Examples:   []string{"model list", "model list --provider OpenAI"},
		Permission: "models",
		Endpoints:  []string{"GET /api/admin/models"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{}
			if v := rt.String("provider"); v != "" {
				pid, err := resolveProviderRef(rt, v)
				if err != nil {
					return err
				}
				q.Set("provider_id", pid)
			}
			path := "/api/admin/models"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			data, _, err := rt.Call(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["models"]) {
				m := asMap(raw)
				rows = append(rows, []string{
					asStr(m["id"]), asStr(m["display_name"]), asStr(m["model_id"]), asStr(m["provider_name"]),
					yesNo(asBoolVal(m["enabled"])), yesNo(asBoolVal(m["hidden"])), yesNo(asBoolVal(m["auto_disabled"])),
				})
			}
			return rt.Table([]string{"id", "display_name", "model_id", "provider", "enabled", "hidden", "auto_disabled"}, rows)
		},
	})

	registerCommand(Command{
		Name:       "model show",
		Group:      "catalogue",
		Summary:    Text{EN: "Show one model", ZH: "显示单个模型"},
		Usage:      "model show <id|name>",
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "model id, display name or upstream model id", ZH: "模型 id、显示名称或上游模型 id"}, Required: true}},
		Examples:   []string{"model show gpt-4o", "model show 01H8X…"},
		Permission: "models",
		Endpoints:  []string{"GET /api/admin/models"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "model id or name")
			if err != nil {
				return err
			}
			mid, err := resolveModelRef(rt, ref)
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/models", nil)
			if err != nil {
				return err
			}
			var m map[string]any
			for _, raw := range asSlice(asMap(data)["models"]) {
				if mm := asMap(raw); asStr(mm["id"]) == mid {
					m = mm
					break
				}
			}
			if m == nil {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有这个模型。")
				}
				return rt.Errorf("no such model.")
			}
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"id", asStr(m["id"])}, {"provider", asStr(m["provider_name"])}, {"model_id", asStr(m["model_id"])},
				{"api_name", asStr(m["api_name"])}, {"display_name", asStr(m["display_name"])},
				{"enabled", yesNo(asBoolVal(m["enabled"]))}, {"hidden", yesNo(asBoolVal(m["hidden"]))},
				{"auto_disabled", yesNo(asBoolVal(m["auto_disabled"]))}, {"route_to_id", asStr(m["route_to_id"])},
				{"reasoning_style", asStr(m["reasoning_style"])}, {"context_window", fmt.Sprint(asNum(m["context_window"]))},
				{"max_output_tokens", fmt.Sprint(asNum(m["max_output_tokens"]))},
				{"request_weight", fmt.Sprint(asNum(m["request_weight"]))},
				{"input_token_weight", fmt.Sprint(asNum(m["input_token_weight"]))},
				{"output_token_weight", fmt.Sprint(asNum(m["output_token_weight"]))},
				{"reasoning_token_weight", fmt.Sprint(asNum(m["reasoning_token_weight"]))},
				{"supports_reasoning", yesNo(asBoolVal(m["supports_reasoning"]))},
				{"supports_images", yesNo(asBoolVal(m["supports_images"]))},
				{"supports_vision", yesNo(asBoolVal(m["supports_vision"]))},
				{"supports_streaming", yesNo(asBoolVal(m["supports_streaming"]))},
				{"supports_tools", yesNo(asBoolVal(m["supports_tools"]))},
				{"supports_image_gen", yesNo(asBoolVal(m["supports_image_gen"]))},
				{"supports_chat_image_gen", yesNo(asBoolVal(m["supports_chat_image_gen"]))},
				{"emulate_tools", yesNo(asBoolVal(m["emulate_tools"]))},
				{"request_override", asStr(m["request_override"])},
				{"sort_order", fmt.Sprint(asNum(m["sort_order"]))}, {"created_at", formatMS(m["created_at"])},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			fmt.Fprintln(rt.Out, "\ngroup grants:")
			var grows [][]string
			for _, raw := range asSlice(m["group_grants"]) {
				gg := asMap(raw)
				grows = append(grows, []string{asStr(gg["group_id"]), asStr(gg["access"])})
			}
			RenderTable(rt.Out, rt.Session.Width, rt.Session.Colour, []string{"group_id", "access"}, grows)
			return nil
		},
	})

	modelFlags := func(createDefaults bool) []Flag {
		def := func(s string) string {
			if createDefaults {
				return s
			}
			return ""
		}
		return []Flag{
			{Name: "--model-id", Hint: Text{EN: "the upstream identifier, required", ZH: "上游模型标识，必填"}, Value: "ID"},
			{Name: "--api-name", Hint: Text{EN: "what the OpenAI-compatible edge calls it, empty = model-id", ZH: "OpenAI 兼容接口对外的名称，留空则等于 model-id"}, Value: "TEXT"},
			{Name: "--display-name", Hint: Text{EN: "1-80 characters", ZH: "1-80 字符"}, Value: "TEXT"},
			{Name: "--description", Hint: Text{EN: "shown to users", ZH: "展示给用户"}, Value: "TEXT"},
			{Name: "--system-prompt", Hint: Text{EN: "empty = use the instance prompt", ZH: "留空则使用实例默认提示词"}, Value: "TEXT"},
			{Name: "--enabled", Hint: Text{EN: "default true", ZH: "默认 true"}, Value: "BOOL", Default: def("true")},
			{Name: "--hidden", Hint: Text{EN: "hide from the model picker without disabling", ZH: "在选择器中隐藏但不禁用"}, Value: "BOOL"},
			{Name: "--sort-order", Hint: Text{EN: "lower sorts first", ZH: "数值越小越靠前"}, Value: "N"},
			{Name: "--route-to", Hint: Text{EN: "another model's ref; empty clears the route", ZH: "另一个模型的引用；留空清除路由"}, Value: "REF"},
			{Name: "--reasoning-style", Hint: Text{EN: "empty = inherit the provider", ZH: "留空则继承服务商设置"}, Value: "STYLE"},
			{Name: "--tiers", Hint: Text{EN: "comma list of id:name:budget", ZH: "逗号分隔的 id:name:budget"}, Value: "LIST"},
			{Name: "--groups", Hint: Text{EN: "comma list of group:access pairs, replaces the set", ZH: "逗号分隔的 group:access 对，整体替换"}, Value: "LIST"},
			{Name: "--context-window", Hint: Text{EN: "tokens", ZH: "token 数"}, Value: "N"},
			{Name: "--max-output-tokens", Hint: Text{EN: "0 = assume 4096 worst-case", ZH: "0 表示按 4096 估算"}, Value: "N"},
			{Name: "--request-weight", Hint: Text{EN: "flat cost per request; an image model's whole price starts here at 0", ZH: "每次请求的固定花费；图像模型的全部价格默认从 0 开始"}, Value: "F"},
			{Name: "--input-token-weight", Hint: Text{EN: "default 1", ZH: "默认 1"}, Value: "F", Default: def("1")},
			{Name: "--output-token-weight", Hint: Text{EN: "default 1", ZH: "默认 1"}, Value: "F", Default: def("1")},
			{Name: "--reasoning-token-weight", Hint: Text{EN: "default 1", ZH: "默认 1"}, Value: "F", Default: def("1")},
			{Name: "--supports-reasoning", Hint: Text{EN: "bool", ZH: "布尔值"}, Value: "BOOL"},
			{Name: "--supports-images", Hint: Text{EN: "bool", ZH: "布尔值"}, Value: "BOOL"},
			{Name: "--supports-vision", Hint: Text{EN: "bool", ZH: "布尔值"}, Value: "BOOL"},
			{Name: "--supports-streaming", Hint: Text{EN: "default true", ZH: "默认 true"}, Value: "BOOL", Default: def("true")},
			{Name: "--supports-system-prompt", Hint: Text{EN: "default true", ZH: "默认 true"}, Value: "BOOL", Default: def("true")},
			{Name: "--supports-tools", Hint: Text{EN: "bool", ZH: "布尔值"}, Value: "BOOL"},
			{Name: "--supports-image-gen", Hint: Text{EN: "bool", ZH: "布尔值"}, Value: "BOOL"},
			{Name: "--supports-chat-image-gen", Hint: Text{EN: "bool", ZH: "布尔值"}, Value: "BOOL"},
			{Name: "--emulate-tools", Hint: Text{EN: "write tools into the prompt", ZH: "把工具写进提示词"}, Value: "BOOL"},
			{Name: "--request-override", Hint: Text{EN: "JSON string for request body overrides", ZH: "请求体覆写的 JSON 字符串"}, Value: "JSON"},
		}
	}

	buildModelBody := func(rt *Runtime) (bodyBuilder, error) {
		body := bodyBuilder{}
		body.str(rt, "model-id", "model_id")
		body.str(rt, "api-name", "api_name")
		body.str(rt, "display-name", "display_name")
		body.str(rt, "description", "description")
		body.str(rt, "system-prompt", "system_prompt")
		body.boolv(rt, "enabled", "enabled")
		body.boolv(rt, "hidden", "hidden")
		body.intv(rt, "sort-order", "sort_order")
		if rt.Present("route-to") {
			if v := rt.String("route-to"); v == "" {
				body["route_to_id"] = ""
			} else {
				rid, err := resolveModelRef(rt, v)
				if err != nil {
					return nil, err
				}
				body["route_to_id"] = rid
			}
		}
		body.str(rt, "reasoning-style", "reasoning_style")
		if rt.Present("tiers") {
			body["reasoning_tiers"] = parseTiers(rt.String("tiers"))
		}
		if rt.Present("groups") {
			grants, err := parseGroupGrants(rt, rt.String("groups"))
			if err != nil {
				return nil, err
			}
			body["group_grants"] = grants
		}
		body.intv(rt, "context-window", "context_window")
		body.intv(rt, "max-output-tokens", "max_output_tokens")
		body.floatv(rt, "request-weight", "request_weight")
		body.floatv(rt, "input-token-weight", "input_token_weight")
		body.floatv(rt, "output-token-weight", "output_token_weight")
		body.floatv(rt, "reasoning-token-weight", "reasoning_token_weight")
		body.boolv(rt, "supports-reasoning", "supports_reasoning")
		body.boolv(rt, "supports-images", "supports_images")
		body.boolv(rt, "supports-vision", "supports_vision")
		body.boolv(rt, "supports-streaming", "supports_streaming")
		body.boolv(rt, "supports-system-prompt", "supports_system_prompt")
		body.boolv(rt, "supports-tools", "supports_tools")
		body.boolv(rt, "supports-image-gen", "supports_image_gen")
		body.boolv(rt, "supports-chat-image-gen", "supports_chat_image_gen")
		body.boolv(rt, "emulate-tools", "emulate_tools")
		body.str(rt, "request-override", "request_override")
		return body, nil
	}

	registerCommand(Command{
		Name:    "model create",
		Group:   "catalogue",
		Summary: Text{EN: "Add a model to a provider's catalogue", ZH: "为服务商新增一个模型"},
		Usage:   "model create --provider REF --model-id ID --display-name NAME [flags]",
		Args:    []Arg{},
		Flags:   append([]Flag{{Name: "--provider", Hint: Text{EN: "provider ref, required", ZH: "服务商引用，必填"}, Value: "REF"}}, modelFlags(true)...),
		Examples: []string{
			"model create --provider OpenAI --model-id gpt-4o --display-name 'GPT-4o'",
			"model create --provider Anthropic --model-id claude-3-7-sonnet --display-name Claude --supports-reasoning true",
		},
		Permission: "models",
		Endpoints:  []string{"POST /api/admin/models"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !rt.Present("provider") || rt.String("provider") == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --provider")
				}
				return rt.Errorf("--provider is required")
			}
			pid, err := resolveProviderRef(rt, rt.String("provider"))
			if err != nil {
				return err
			}
			body, err := buildModelBody(rt)
			if err != nil {
				return err
			}
			body["provider_id"] = pid
			data, _, err := rt.Call(http.MethodPost, "/api/admin/models", map[string]any(body))
			if err != nil {
				return err
			}
			m := asMap(asMap(data)["model"])
			return rt.Fields([][2]string{{"id", asStr(m["id"])}, {"display_name", asStr(m["display_name"])}, {"created_at", formatMS(m["created_at"])}})
		},
	})

	registerCommand(Command{
		Name:    "model edit",
		Group:   "catalogue",
		Summary: Text{EN: "Edit a model", ZH: "编辑模型"},
		Usage:   "model edit <id|name> [flags]",
		Help: Text{
			EN: "Only the flags you give are changed. The model's provider cannot be changed through " +
				"this command.",
			ZH: "只会修改你给出的选项。此命令不能更改模型所属的服务商。",
		},
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "model id, display name or upstream model id", ZH: "模型 id、显示名称或上游模型 id"}, Required: true}},
		Flags:      modelFlags(false),
		Examples:   []string{"model edit gpt-4o --enabled false", "model edit gpt-4o --sort-order 1"},
		Permission: "models",
		Endpoints:  []string{"PATCH /api/admin/models/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "model id or name")
			if err != nil {
				return err
			}
			mid, err := resolveModelRef(rt, ref)
			if err != nil {
				return err
			}
			body, err := buildModelBody(rt)
			if err != nil {
				return err
			}
			if len(body) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有需要修改的内容：请至少给出一个选项")
				}
				return rt.Errorf("nothing to change: give at least one flag")
			}
			data, _, err := rt.Call(http.MethodPatch, "/api/admin/models/"+url.PathEscape(mid), map[string]any(body))
			if err != nil {
				return err
			}
			m := asMap(asMap(data)["model"])
			return rt.Fields([][2]string{{"id", asStr(m["id"])}, {"display_name", asStr(m["display_name"])}, {"updated_at", formatMS(m["updated_at"])}})
		},
	})

	registerCommand(Command{
		Name:        "model delete",
		Group:       "catalogue",
		Summary:     Text{EN: "Delete a model", ZH: "删除模型"},
		Usage:       "model delete <id|name> --yes",
		Args:        []Arg{{Name: "id|name", Hint: Text{EN: "model id, display name or upstream model id", ZH: "模型 id、显示名称或上游模型 id"}, Required: true}},
		Examples:    []string{"model delete old-model --yes", "model delete old-model -y"},
		Permission:  "models",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/models/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "model id or name")
			if err != nil {
				return err
			}
			mid, err := resolveModelRef(rt, ref)
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/models/"+url.PathEscape(mid), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已删除。\n")
			} else {
				rt.Printf("deleted.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "model order",
		Group:   "catalogue",
		Summary: Text{EN: "Reorder models", ZH: "调整模型顺序"},
		Usage:   "model order <ref1> <ref2> ...",
		Help: Text{
			EN: "Lists the models in the order they should read; position becomes sort_order. A model " +
				"left out keeps its current sort_order, which may then collide with the ones given.",
			ZH: "按希望的展示顺序列出模型；位置即写入 sort_order。未列出的模型保留原有 sort_order，" +
				"可能因此与列出的模型顺序冲突。",
		},
		Args:       []Arg{{Name: "refs...", Hint: Text{EN: "model refs, in the new order", ZH: "模型引用，按新顺序排列"}, Required: true}},
		Examples:   []string{"model order gpt-4o gpt-4o-mini claude", "model order 01H8X… 01H8Y…"},
		Permission: "models",
		Endpoints:  []string{"PUT /api/admin/models/order"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("至少需要一个模型引用")
				}
				return rt.Errorf("at least one model ref is required")
			}
			var ids []string
			for _, ref := range rt.Args() {
				mid, err := resolveModelRef(rt, ref)
				if err != nil {
					return err
				}
				ids = append(ids, mid)
			}
			if _, _, err := rt.Call(http.MethodPut, "/api/admin/models/order", map[string]any{"ids": ids}); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已更新排序。\n")
			} else {
				rt.Printf("order updated.\n")
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "model import",
		Group:   "catalogue",
		Summary: Text{EN: "Bulk import models from a JSON document", ZH: "从 JSON 文档批量导入模型"},
		Usage:   "model import <json>",
		Help: Text{
			EN: "The argument is the whole request body — {\"models\":[…]} — matched by name against " +
				"providers, route targets and groups, not by id. Paste it as one quoted argument; " +
				"Shift+Enter in the web terminal lets you spread it over several lines first. One bad " +
				"entry does not abort the rest; see the response's skipped list.",
			ZH: "参数是完整的请求体 —— {\"models\":[…]} —— 其中服务商、路由目标和分组均按名称而非 id 匹配。" +
				"作为一个带引号的参数粘贴；网页终端中可先用 Shift+Enter 分行输入。" +
				"其中一条有问题不会中止其余条目，请查看返回结果的 skipped 列表。",
		},
		Args:       []Arg{{Name: "json", Hint: Text{EN: "the import document", ZH: "导入文档"}, Required: true}},
		Examples:   []string{`model import '{"models":[{"provider":"OpenAI","model_id":"gpt-4o","display_name":"GPT-4o"}]}'`, "model import --json '{...}'"},
		Permission: "models",
		Endpoints:  []string{"POST /api/admin/models/import"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.NArg() == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要一份 JSON 文档")
				}
				return rt.Errorf("a JSON document is required")
			}
			body, err := parseJSONBody(rt, strings.Join(rt.Args(), " "))
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/models/import", body)
			if err != nil {
				return err
			}
			m := asMap(data)
			var skipped []string
			for _, s := range asSlice(m["skipped"]) {
				skipped = append(skipped, asStr(s))
			}
			return rt.Fields([][2]string{
				{"created", fmt.Sprint(asNum(m["created"]))}, {"updated", fmt.Sprint(asNum(m["updated"]))},
				{"skipped", strings.Join(skipped, "; ")},
			})
		},
	})
}

// parseGroupGrants turns "group-a:use,group-b:view" into the
// []{group_id,access} shape POST/PATCH /api/admin/models takes for
// group_grants, resolving each left-hand side through resolveGroupRef.
func parseGroupGrants(rt *Runtime, raw string) ([]map[string]any, error) {
	var out []map[string]any
	for _, entry := range splitCSV(raw) {
		ref, access, ok := strings.Cut(entry, ":")
		if !ok {
			access = "use"
		}
		gid, err := resolveGroupRef(rt, strings.TrimSpace(ref))
		if err != nil {
			return nil, err
		}
		access = strings.TrimSpace(access)
		if access == "" {
			access = "use"
		}
		out = append(out, map[string]any{"group_id": gid, "access": access})
	}
	return out, nil
}
