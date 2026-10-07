package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The code sandbox from the terminal: the same profiles, interpreters and
// runners the backoffice page edits, through the same routes.
//
// An interpreter is uploaded as base64 on the line rather than from a path:
// the terminal runs on the server's side of the connection, so a path would
// name a file on the server, not on the machine of whoever is typing.

// resolveProfileRef accepts a profile's id or its exact name, case-
// insensitively, and refuses to guess between two that tie.
func resolveProfileRef(rt *Runtime, ref string) (string, map[string]any, error) {
	data, _, err := rt.Call(http.MethodGet, "/api/admin/sandbox/profiles", nil)
	if err != nil {
		return "", nil, err
	}
	var found map[string]any
	matches := 0
	for _, raw := range asSlice(asMap(data)["profiles"]) {
		p := asMap(raw)
		if asStr(p["id"]) == ref {
			return ref, p, nil
		}
		if strings.EqualFold(asStr(p["name"]), ref) {
			found = p
			matches++
		}
	}
	if matches == 1 {
		return asStr(found["id"]), found, nil
	}
	if rt.Session.Lang == "zh" {
		return "", nil, rt.Errorf("没有这个沙箱配置：%s", ref)
	}
	return "", nil, rt.Errorf("no such sandbox profile: %s", ref)
}

// parseImages reads "python=python:3.12-slim,javascript=node:22" into the
// language-to-image (or interpreter id) map, cutting at the first '=' so an
// image tag's own characters survive.
func parseImages(rt *Runtime, raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range splitCSV(raw) {
		language, target, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(language) == "" {
			return nil, rt.Errorf("--images takes language=image, got %q", pair)
		}
		out[strings.TrimSpace(language)] = strings.TrimSpace(target)
	}
	return out, nil
}

var profileFlags = []Flag{
	{Name: "--name", Hint: Text{EN: "1-40 characters", ZH: "1-40 字符"}, Value: "NAME"},
	{Name: "--kind", Hint: Text{EN: "wasm (in-process) or runner (Docker)", ZH: "wasm（进程内）或 runner（Docker）"}, Value: "KIND"},
	{Name: "--languages", Hint: Text{EN: "comma list of languages", ZH: "逗号分隔的语言"}, Value: "LIST"},
	{Name: "--images", Hint: Text{EN: "language=image (runner) or language=interpreter-id (wasm), comma list", ZH: "language=镜像（runner）或 language=解释器 id（wasm），逗号分隔"}, Value: "LIST"},
	{Name: "--timeout-ms", Hint: Text{EN: "time limit per run", ZH: "每次运行的时间上限"}, Value: "N"},
	{Name: "--memory-mb", Hint: Text{EN: "memory limit per run", ZH: "每次运行的内存上限"}, Value: "N"},
	{Name: "--max-output-bytes", Hint: Text{EN: "stdout and stderr together", ZH: "stdout 与 stderr 合计"}, Value: "N"},
	{Name: "--max-concurrent", Hint: Text{EN: "0 = no per-profile limit", ZH: "0 表示不单独限制"}, Value: "N"},
	{Name: "--enabled", Hint: Text{EN: "whether groups pointing at it may run code", ZH: "指向它的分组能否运行代码"}, Value: "BOOL"},
}

// applyProfileFlags overlays the flags given on body. Every write route
// takes the whole profile, so an edit starts from the current one and only
// what was typed changes.
func applyProfileFlags(rt *Runtime, body bodyBuilder) error {
	body.str(rt, "name", "name")
	body.str(rt, "kind", "kind")
	if rt.Present("languages") {
		body["languages"] = splitCSV(rt.String("languages"))
	}
	if rt.Present("images") {
		images, err := parseImages(rt, rt.String("images"))
		if err != nil {
			return err
		}
		body["images"] = images
	}
	body.int64v(rt, "timeout-ms", "timeout_ms")
	body.int64v(rt, "memory-mb", "memory_mb")
	body.int64v(rt, "max-output-bytes", "max_output_bytes")
	body.int64v(rt, "max-concurrent", "max_concurrent")
	body.boolv(rt, "enabled", "enabled")
	return nil
}

func profileFields(p map[string]any) [][2]string {
	var images []string
	for language, target := range asMap(p["images"]) {
		images = append(images, language+"="+asStr(target))
	}
	var languages []string
	for _, l := range asSlice(p["languages"]) {
		languages = append(languages, asStr(l))
	}
	return [][2]string{
		{"id", asStr(p["id"])}, {"name", asStr(p["name"])}, {"kind", asStr(p["kind"])},
		{"languages", strings.Join(languages, ",")}, {"images", strings.Join(images, ",")},
		{"timeout_ms", fmt.Sprint(asNum(p["timeout_ms"]))}, {"memory_mb", fmt.Sprint(asNum(p["memory_mb"]))},
		{"max_output_bytes", fmt.Sprint(asNum(p["max_output_bytes"]))},
		{"max_concurrent", fmt.Sprint(asNum(p["max_concurrent"]))},
		{"enabled", yesNo(asBoolVal(p["enabled"]))},
	}
}

func init() {
	registerCommand(Command{
		Name:       "sandbox profile list",
		Group:      "sandbox",
		Summary:    Text{EN: "List code sandbox profiles", ZH: "列出代码沙箱配置"},
		Usage:      "sandbox profile list",
		Examples:   []string{"sandbox profile list", "sandbox profile list --json"},
		Permission: "sandbox",
		Endpoints:  []string{"GET /api/admin/sandbox/profiles"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/sandbox/profiles", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["profiles"]) {
				p := asMap(raw)
				var languages []string
				for _, l := range asSlice(p["languages"]) {
					languages = append(languages, asStr(l))
				}
				rows = append(rows, []string{asStr(p["id"]), asStr(p["name"]), asStr(p["kind"]),
					strings.Join(languages, ","), yesNo(asBoolVal(p["enabled"]))})
			}
			return rt.Table([]string{"id", "name", "kind", "languages", "enabled"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "sandbox profile create",
		Group:   "sandbox",
		Summary: Text{EN: "Create a code sandbox profile", ZH: "创建代码沙箱配置"},
		Usage:   "sandbox profile create --name NAME --kind wasm|runner [flags]",
		Flags:   profileFlags,
		Examples: []string{
			"sandbox profile create --name Docker --kind runner --languages python --images python=python:3.12-slim --enabled",
			"sandbox profile create --name Light --kind wasm --languages javascript --images javascript=01H8X…",
		},
		Permission: "sandbox",
		Endpoints:  []string{"POST /api/admin/sandbox/profiles"},
		Run: func(_ context.Context, rt *Runtime) error {
			body := bodyBuilder{}
			if err := applyProfileFlags(rt, body); err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/sandbox/profiles", map[string]any(body))
			if err != nil {
				return err
			}
			return rt.Fields(profileFields(asMap(asMap(data)["profile"])))
		},
	})

	registerCommand(Command{
		Name:    "sandbox profile edit",
		Group:   "sandbox",
		Summary: Text{EN: "Edit a code sandbox profile", ZH: "编辑代码沙箱配置"},
		Usage:   "sandbox profile edit <id|name> [flags]",
		Help: Text{
			EN: "Only the flags you give change. --languages and --images, when given, replace the whole list.",
			ZH: "只修改给出的选项。给出 --languages 或 --images 时会整体替换。",
		},
		Args:       []Arg{{Name: "id|name", Hint: Text{EN: "profile id or name", ZH: "配置 id 或名称"}, Required: true}},
		Flags:      profileFlags,
		Examples:   []string{"sandbox profile edit Docker --enabled=false", "sandbox profile edit Docker --timeout-ms 20000"},
		Permission: "sandbox",
		Endpoints:  []string{"GET /api/admin/sandbox/profiles", "PUT /api/admin/sandbox/profiles/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "profile id or name")
			if err != nil {
				return err
			}
			pid, current, err := resolveProfileRef(rt, ref)
			if err != nil {
				return err
			}
			// Copied field by field rather than sent back whole: the route
			// refuses fields it does not take, and the record carries its id
			// and timestamps.
			body := bodyBuilder{}
			for _, key := range []string{"name", "kind", "languages", "images", "timeout_ms",
				"memory_mb", "max_output_bytes", "max_concurrent", "enabled"} {
				body[key] = current[key]
			}
			if err := applyProfileFlags(rt, body); err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPut, "/api/admin/sandbox/profiles/"+url.PathEscape(pid), map[string]any(body))
			if err != nil {
				return err
			}
			return rt.Fields(profileFields(asMap(asMap(data)["profile"])))
		},
	})

	registerCommand(Command{
		Name:    "sandbox profile delete",
		Group:   "sandbox",
		Summary: Text{EN: "Delete a code sandbox profile", ZH: "删除代码沙箱配置"},
		Usage:   "sandbox profile delete <id|name> --yes",
		Help: Text{
			EN: "Groups pointing at it lose the sandbox, and its runners and jobs go with it.",
			ZH: "指向它的分组将失去沙箱，其 runner 和任务一并删除。",
		},
		Args:        []Arg{{Name: "id|name", Hint: Text{EN: "profile id or name", ZH: "配置 id 或名称"}, Required: true}},
		Examples:    []string{"sandbox profile delete Docker --yes", "sandbox profile delete 01H8X… -y"},
		Permission:  "sandbox",
		Destructive: true,
		Endpoints:   []string{"GET /api/admin/sandbox/profiles", "DELETE /api/admin/sandbox/profiles/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "profile id or name")
			if err != nil {
				return err
			}
			pid, _, err := resolveProfileRef(rt, ref)
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/sandbox/profiles/"+url.PathEscape(pid), nil); err != nil {
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
		Name:    "sandbox profile test",
		Group:   "sandbox",
		Summary: Text{EN: "Run a program under a profile to check it works", ZH: "在某个配置下运行程序以检查是否可用"},
		Usage:   "sandbox profile test <id|name> --language L --code CODE [--stdin TEXT]",
		Help: Text{
			EN: "Runs even when the profile or the instance-wide switch is off: the point is to find out before anybody is pointed at it.",
			ZH: "即使配置或全局开关关闭也会运行：目的是在分配给任何人之前先确认可用。",
		},
		Args: []Arg{{Name: "id|name", Hint: Text{EN: "profile id or name", ZH: "配置 id 或名称"}, Required: true}},
		Flags: []Flag{
			{Name: "--language", Hint: Text{EN: "one of the profile's languages", ZH: "配置中的某种语言"}, Value: "L"},
			{Name: "--code", Hint: Text{EN: "the program", ZH: "程序代码"}, Value: "CODE"},
			{Name: "--stdin", Hint: Text{EN: "standard input", ZH: "标准输入"}, Value: "TEXT"},
		},
		Examples:   []string{"sandbox profile test Docker --language python --code 'print(1+1)'", "sandbox profile test Light --language javascript --code 'console.log(2)'"},
		Permission: "sandbox",
		Endpoints:  []string{"GET /api/admin/sandbox/profiles", "POST /api/admin/sandbox/profiles/{id}/test"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "profile id or name")
			if err != nil {
				return err
			}
			pid, _, err := resolveProfileRef(rt, ref)
			if err != nil {
				return err
			}
			body := map[string]any{"language": rt.String("language"), "code": rt.String("code"), "stdin": rt.String("stdin")}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/sandbox/profiles/"+url.PathEscape(pid)+"/test", body)
			if err != nil {
				return err
			}
			r := asMap(asMap(data)["result"])
			return rt.Fields([][2]string{
				{"exit_code", fmt.Sprint(asNum(r["exit_code"]))}, {"duration_ms", fmt.Sprint(asNum(r["duration_ms"]))},
				{"timed_out", yesNo(asBoolVal(r["timed_out"]))}, {"crashed", yesNo(asBoolVal(r["crashed"]))},
				{"truncated", yesNo(asBoolVal(r["truncated"]))},
				{"stdout", asStr(r["stdout"])}, {"stderr", asStr(r["stderr"])},
			})
		},
	})

	registerCommand(Command{
		Name:       "sandbox interpreter list",
		Group:      "sandbox",
		Summary:    Text{EN: "List uploaded WASI interpreters", ZH: "列出已上传的 WASI 解释器"},
		Usage:      "sandbox interpreter list",
		Examples:   []string{"sandbox interpreter list", "sandbox interpreter list --json"},
		Permission: "sandbox",
		Endpoints:  []string{"GET /api/admin/sandbox/interpreters"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/sandbox/interpreters", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["interpreters"]) {
				i := asMap(raw)
				rows = append(rows, []string{asStr(i["id"]), asStr(i["name"]), asStr(i["language"]),
					asStr(i["version"]), fmt.Sprint(asNum(i["size_bytes"])), formatMS(i["created_at"])})
			}
			return rt.Table([]string{"id", "name", "language", "version", "bytes", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "sandbox interpreter upload",
		Group:   "sandbox",
		Summary: Text{EN: "Upload a WASI interpreter", ZH: "上传 WASI 解释器"},
		Usage:   "sandbox interpreter upload --name NAME --language L --module-base64 DATA [--args LIST] [--version V]",
		Help: Text{
			EN: "--args is the argv after the program name; {file} becomes the path of the code, and without it the code is given on stdin. " +
				"The module is base64 because a path here would name a file on the server.",
			ZH: "--args 是程序名之后的参数；{file} 会替换为代码路径，不含 {file} 时代码经 stdin 传入。" +
				"模块用 base64 传入，因为这里的路径指向的是服务器上的文件。",
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-40 characters", ZH: "1-40 字符"}, Value: "NAME"},
			{Name: "--language", Hint: Text{EN: "the language it runs", ZH: "它运行的语言"}, Value: "L"},
			{Name: "--version", Hint: Text{EN: "free text", ZH: "自由填写"}, Value: "V"},
			{Name: "--args", Hint: Text{EN: "comma list, e.g. {file}", ZH: "逗号分隔，如 {file}"}, Value: "LIST"},
			// Sensitive so the audit line records that a module was sent,
			// not megabytes of it.
			{Name: "--module-base64", Hint: Text{EN: "the .wasm file, base64", ZH: ".wasm 文件的 base64"}, Value: "DATA", Sensitive: true},
		},
		Examples:   []string{"sandbox interpreter upload --name QuickJS --language javascript --args {file} --module-base64 AGFzbQ…", "sandbox interpreter upload --name Lua --language lua --module-base64 AGFzbQ…"},
		Permission: "sandbox",
		Endpoints:  []string{"POST /api/admin/sandbox/interpreters"},
		Run: func(_ context.Context, rt *Runtime) error {
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			body.str(rt, "language", "language")
			body.str(rt, "version", "version")
			if rt.Present("args") {
				body["args"] = splitCSV(rt.String("args"))
			}
			body["module"] = strings.TrimSpace(rt.String("module-base64"))
			data, _, err := rt.Call(http.MethodPost, "/api/admin/sandbox/interpreters", map[string]any(body))
			if err != nil {
				return err
			}
			i := asMap(asMap(data)["interpreter"])
			return rt.Fields([][2]string{{"id", asStr(i["id"])}, {"name", asStr(i["name"])},
				{"sha256", asStr(i["sha256"])}, {"size_bytes", fmt.Sprint(asNum(i["size_bytes"]))}})
		},
	})

	registerCommand(Command{
		Name:        "sandbox interpreter delete",
		Group:       "sandbox",
		Summary:     Text{EN: "Delete an uploaded interpreter", ZH: "删除已上传的解释器"},
		Usage:       "sandbox interpreter delete <id> --yes",
		Help:        Text{EN: "Profiles mapping a language to it stop being able to run that language.", ZH: "将某语言映射到它的配置将无法再运行该语言。"},
		Args:        []Arg{{Name: "id", Hint: Text{EN: "interpreter id", ZH: "解释器 id"}, Required: true}},
		Examples:    []string{"sandbox interpreter delete 01H8X… --yes", "sandbox interpreter delete 01H8X… -y"},
		Permission:  "sandbox",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/sandbox/interpreters/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "interpreter id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/sandbox/interpreters/"+url.PathEscape(ref), nil); err != nil {
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
		Name:       "sandbox runner list",
		Group:      "sandbox",
		Summary:    Text{EN: "List Docker runners", ZH: "列出 Docker runner"},
		Usage:      "sandbox runner list",
		Examples:   []string{"sandbox runner list", "sandbox runner list --json"},
		Permission: "sandbox",
		Endpoints:  []string{"GET /api/admin/sandbox/runners"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/sandbox/runners", nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["runners"]) {
				r := asMap(raw)
				rows = append(rows, []string{asStr(r["id"]), asStr(r["name"]), asStr(r["profile_id"]),
					asStr(r["prefix"]), formatMS(r["last_seen_at"])})
			}
			return rt.Table([]string{"id", "name", "profile", "prefix", "last_seen"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "sandbox runner create",
		Group:   "sandbox",
		Summary: Text{EN: "Issue a token for a Docker runner", ZH: "为 Docker runner 签发令牌"},
		Usage:   "sandbox runner create --profile <id|name> [--name NAME]",
		Help: Text{
			EN: "The token is shown once. Give it to arc-sandbox-runner as ARC_RUNNER_TOKEN.",
			ZH: "令牌只显示一次，作为 ARC_RUNNER_TOKEN 交给 arc-sandbox-runner。",
		},
		Flags: []Flag{
			{Name: "--profile", Hint: Text{EN: "a runner profile's id or name", ZH: "runner 类型配置的 id 或名称"}, Value: "REF"},
			{Name: "--name", Hint: Text{EN: "what to call the machine", ZH: "机器名称"}, Value: "NAME"},
		},
		Examples:   []string{"sandbox runner create --profile Docker --name box-1", "sandbox runner create --profile Docker"},
		Permission: "sandbox",
		Endpoints:  []string{"GET /api/admin/sandbox/profiles", "POST /api/admin/sandbox/runners"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.String("profile") == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("需要 --profile")
				}
				return rt.Errorf("--profile is required")
			}
			pid, _, err := resolveProfileRef(rt, rt.String("profile"))
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/sandbox/runners",
				map[string]any{"profile_id": pid, "name": rt.String("name")})
			if err != nil {
				return err
			}
			r := asMap(asMap(data)["runner"])
			return rt.Fields([][2]string{{"id", asStr(r["id"])}, {"name", asStr(r["name"])},
				{"token", asStr(asMap(data)["token"])}})
		},
	})

	registerCommand(Command{
		Name:        "sandbox runner delete",
		Group:       "sandbox",
		Summary:     Text{EN: "Revoke a Docker runner", ZH: "吊销 Docker runner"},
		Usage:       "sandbox runner delete <id> --yes",
		Help:        Text{EN: "A job it held is queued again when its lease runs out.", ZH: "它持有的任务会在租约到期后重新排队。"},
		Args:        []Arg{{Name: "id", Hint: Text{EN: "runner id", ZH: "runner id"}, Required: true}},
		Examples:    []string{"sandbox runner delete 01H8X… --yes", "sandbox runner delete 01H8X… -y"},
		Permission:  "sandbox",
		Destructive: true,
		Endpoints:   []string{"DELETE /api/admin/sandbox/runners/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "runner id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/admin/sandbox/runners/"+url.PathEscape(ref), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已吊销。\n")
			} else {
				rt.Printf("revoked.\n")
			}
			return nil
		},
	})
}
