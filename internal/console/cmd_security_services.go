package console

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// Mail relay and disposable-address screening share the security grant with
// the admin pages that configure them; credentials are write-only in both.
func init() {
	registerCommand(Command{
		Name:    "mail show",
		Group:   "instance",
		Summary: Text{EN: "Show outgoing mail settings", ZH: "查看发信配置"},
		Usage:   "mail show",
		Help: Text{
			EN: "Shows the configured SMTP connection and public link origin. The password itself is " +
				"never returned; password_set only says whether one is stored.",
			ZH: "显示 SMTP 连接和公开链接地址。服务器不会返回密码本身；password_set 只表示是否已保存密码。",
		},
		Examples:   []string{"mail show", "mail show --json"},
		SeeAlso:    []string{"mail set", "mail test"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/mail"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/mail", nil)
			if err != nil {
				return err
			}
			return rt.Fields(mailFields(asMap(data)))
		},
	})

	registerCommand(Command{
		Name:    "mail set",
		Group:   "instance",
		Summary: Text{EN: "Configure outgoing mail", ZH: "配置发信服务"},
		Usage:   "mail set [flags]",
		Help: Text{
			EN: "Changes only the fields you give. Existing settings are read first so omitted values " +
				"stay intact. The SMTP password is write-only: omit --password to keep it, or use " +
				"--clear-password to remove it.",
			ZH: "只修改给出的字段。命令会先读取现有配置，因此未提供的值保持不变。SMTP 密码只写不读：" +
				"省略 --password 会保留原密码，使用 --clear-password 可清除密码。",
		},
		Flags: []Flag{
			{Name: "--host", Hint: Text{EN: "SMTP host", ZH: "SMTP 主机名"}, Value: "HOST"},
			{Name: "--port", Hint: Text{EN: "SMTP port", ZH: "SMTP 端口"}, Value: "PORT"},
			{Name: "--username", Hint: Text{EN: "SMTP username", ZH: "SMTP 用户名"}, Value: "USER"},
			{Name: "--from", Hint: Text{EN: "sender address", ZH: "发件地址"}, Value: "EMAIL"},
			{Name: "--implicit-tls", Hint: Text{EN: "whether TLS starts immediately: true or false", ZH: "是否从连接开始即使用 TLS：true 或 false"}, Value: "BOOL"},
			{Name: "--public-url", Hint: Text{EN: "HTTPS origin used in verification links", ZH: "验证链接使用的 HTTPS 地址"}, Value: "URL"},
			{Name: "--password", Hint: Text{EN: "SMTP password", ZH: "SMTP 密码"}, Value: "SECRET", Sensitive: true},
			{Name: "--clear-password", Hint: Text{EN: "remove the stored password", ZH: "清除已保存的密码"}},
		},
		Examples: []string{
			"mail set --host smtp.example.com --port 587 --from arc@example.com --public-url https://arc.example.com",
			"mail set --username arc --password 'secret' --implicit-tls false",
		},
		SeeAlso:    []string{"mail show", "mail test"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/mail", "PUT /api/admin/mail"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !mailSetRequested(rt) {
				return rt.Errorf("give at least one mail setting to change")
			}
			if rt.Present("password") && rt.Bool("clear-password") {
				return rt.Errorf("use --password or --clear-password, not both")
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/mail", nil)
			if err != nil {
				return err
			}
			current := asMap(data)
			body := map[string]any{
				"host":           asStr(current["host"]),
				"port":           int(asNum(current["port"])),
				"username":       asStr(current["username"]),
				"from":           asStr(current["from"]),
				"implicit_tls":   asBoolVal(current["implicit_tls"]),
				"public_url":     asStr(current["public_url"]),
				"password":       rt.String("password"),
				"clear_password": rt.Bool("clear-password"),
			}
			for _, field := range []string{"host", "username", "from", "public-url"} {
				if rt.Present(field) {
					key := field
					if field == "public-url" {
						key = "public_url"
					}
					body[key] = rt.String(field)
				}
			}
			if rt.Present("port") {
				port, err := strconv.Atoi(rt.String("port"))
				if err != nil {
					return rt.Errorf("--port must be a whole number")
				}
				body["port"] = port
			}
			if rt.Present("implicit-tls") {
				value, err := strconv.ParseBool(rt.String("implicit-tls"))
				if err != nil {
					return rt.Errorf("--implicit-tls must be true or false")
				}
				body["implicit_tls"] = value
			}
			updated, _, err := rt.Call(http.MethodPut, "/api/admin/mail", body)
			if err != nil {
				return err
			}
			return rt.Fields(mailFields(asMap(updated)))
		},
	})

	registerCommand(Command{
		Name:    "mail test",
		Group:   "instance",
		Summary: Text{EN: "Send a test email", ZH: "发送测试邮件"},
		Usage:   "mail test <recipient>",
		Help: Text{
			EN: "Sends one test message through the active SMTP settings. The server permits one test " +
				"message every five minutes.",
			ZH: "使用当前 SMTP 配置发送一封测试邮件。服务器每五分钟允许发送一封测试邮件。",
		},
		Args:       []Arg{{Name: "recipient", Hint: Text{EN: "email address", ZH: "邮箱地址"}, Required: true}},
		Examples:   []string{"mail test me@example.com", "mail test admin@example.com --json"},
		SeeAlso:    []string{"mail show", "mail set"},
		Permission: "security",
		Endpoints:  []string{"POST /api/admin/mail/test"},
		Run: func(_ context.Context, rt *Runtime) error {
			to, err := requireRef(rt, "recipient email address")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/mail/test", map[string]any{"to": to})
			if err != nil {
				return err
			}
			return rt.Fields([][2]string{{"sent", yesNo(asBoolVal(asMap(data)["sent"]))}})
		},
	})

	registerCommand(Command{
		Name:    "usercheck show",
		Group:   "instance",
		Summary: Text{EN: "Show disposable-address screening settings", ZH: "查看临时邮箱筛查配置"},
		Usage:   "usercheck show",
		Help: Text{
			EN: "Shows whether screening is on, exempt domains, the failure mode and whether the API " +
				"key is stored. The key itself is never returned.",
			ZH: "显示筛查开关、豁免域名、失败处理方式和 API 密钥是否已保存。服务器不会返回密钥本身。",
		},
		Examples:   []string{"usercheck show", "usercheck show --json"},
		SeeAlso:    []string{"usercheck set", "usercheck test"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/usercheck"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/admin/usercheck", nil)
			if err != nil {
				return err
			}
			return rt.Fields(userCheckFields(asMap(data)))
		},
	})

	registerCommand(Command{
		Name:    "usercheck set",
		Group:   "instance",
		Summary: Text{EN: "Configure disposable-address screening", ZH: "配置临时邮箱筛查"},
		Usage:   "usercheck set [flags]",
		Help: Text{
			EN: "Changes only the fields you give. Exempt domains are a comma-separated list. The API " +
				"key is write-only: omit --api-key to keep it, or use --clear-api-key to remove it. " +
				"Failure mode is allow or reject when the screening service is unavailable.",
			ZH: "只修改给出的字段，豁免域名用逗号分隔。API 密钥只写不读：省略 --api-key 会保留原密钥，" +
				"使用 --clear-api-key 可清除。筛查服务不可用时，可选 allow 或 reject。",
		},
		Flags: []Flag{
			{Name: "--enabled", Hint: Text{EN: "whether screening is enabled: true or false", ZH: "是否启用筛查：true 或 false"}, Value: "BOOL"},
			{Name: "--exempt-domains", Hint: Text{EN: "comma-separated domains", ZH: "逗号分隔的域名"}, Value: "DOMAINS"},
			{Name: "--failure-mode", Hint: Text{EN: "allow or reject", ZH: "allow 或 reject"}, Value: "MODE"},
			{Name: "--api-key", Hint: Text{EN: "UserCheck API key", ZH: "UserCheck API 密钥"}, Value: "SECRET", Sensitive: true},
			{Name: "--clear-api-key", Hint: Text{EN: "remove the stored API key", ZH: "清除已保存的 API 密钥"}},
		},
		Examples: []string{
			"usercheck set --enabled true --failure-mode reject",
			"usercheck set --exempt-domains example.com,example.net --api-key 'secret'",
		},
		SeeAlso:    []string{"usercheck show", "usercheck test"},
		Permission: "security",
		Endpoints:  []string{"GET /api/admin/usercheck", "PUT /api/admin/usercheck"},
		Run: func(_ context.Context, rt *Runtime) error {
			if !userCheckSetRequested(rt) {
				return rt.Errorf("give at least one UserCheck setting to change")
			}
			if rt.Present("api-key") && rt.Bool("clear-api-key") {
				return rt.Errorf("use --api-key or --clear-api-key, not both")
			}
			data, _, err := rt.Call(http.MethodGet, "/api/admin/usercheck", nil)
			if err != nil {
				return err
			}
			current := asMap(data)
			domains := make([]string, 0)
			for _, raw := range asSlice(current["exempt_domains"]) {
				if domain, ok := raw.(string); ok {
					domains = append(domains, domain)
				}
			}
			body := map[string]any{
				"enabled":        asBoolVal(current["enabled"]),
				"exempt_domains": domains,
				"failure_mode":   asStr(current["failure_mode"]),
				"api_key":        rt.String("api-key"),
				"clear_api_key":  rt.Bool("clear-api-key"),
			}
			if rt.Present("enabled") {
				value, err := strconv.ParseBool(rt.String("enabled"))
				if err != nil {
					return rt.Errorf("--enabled must be true or false")
				}
				body["enabled"] = value
			}
			if rt.Present("exempt-domains") {
				body["exempt_domains"] = splitDomainList(rt.String("exempt-domains"))
			}
			if rt.Present("failure-mode") {
				body["failure_mode"] = rt.String("failure-mode")
			}
			updated, _, err := rt.Call(http.MethodPut, "/api/admin/usercheck", body)
			if err != nil {
				return err
			}
			return rt.Fields(userCheckFields(asMap(updated)))
		},
	})

	registerCommand(Command{
		Name:    "usercheck test",
		Group:   "instance",
		Summary: Text{EN: "Check an email address with UserCheck", ZH: "使用 UserCheck 检查邮箱"},
		Usage:   "usercheck test <email>",
		Help: Text{
			EN: "Runs one live test through the saved API key and reports whether the address is " +
				"disposable or was skipped. The server permits one test every five minutes.",
			ZH: "使用已保存的 API 密钥执行一次实时检测，并报告该地址是否为临时邮箱或是否跳过检查。" +
				"服务器每五分钟允许执行一次测试。",
		},
		Args:       []Arg{{Name: "email", Hint: Text{EN: "email address", ZH: "邮箱地址"}, Required: true}},
		Examples:   []string{"usercheck test person@example.com", "usercheck test disposable@example.test --json"},
		SeeAlso:    []string{"usercheck show", "usercheck set"},
		Permission: "security",
		Endpoints:  []string{"POST /api/admin/usercheck/test"},
		Run: func(_ context.Context, rt *Runtime) error {
			email, err := requireRef(rt, "email address")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodPost, "/api/admin/usercheck/test", map[string]any{"email": email})
			if err != nil {
				return err
			}
			result := asMap(data)
			return rt.Fields([][2]string{
				{"disposable", yesNo(asBoolVal(result["disposable"]))},
				{"skipped", yesNo(asBoolVal(result["skipped"]))},
			})
		},
	})
}

func mailSetRequested(rt *Runtime) bool {
	for _, name := range []string{"host", "port", "username", "from", "implicit-tls", "public-url", "password", "clear-password"} {
		if rt.Present(name) {
			return true
		}
	}
	return false
}

func mailFields(m map[string]any) [][2]string {
	return [][2]string{
		{"host", asStr(m["host"])},
		{"port", strconv.Itoa(int(asNum(m["port"])))},
		{"username", asStr(m["username"])},
		{"from", asStr(m["from"])},
		{"implicit_tls", yesNo(asBoolVal(m["implicit_tls"]))},
		{"public_url", asStr(m["public_url"])},
		{"password_set", yesNo(asBoolVal(m["password_set"]))},
	}
}

func userCheckSetRequested(rt *Runtime) bool {
	for _, name := range []string{"enabled", "exempt-domains", "failure-mode", "api-key", "clear-api-key"} {
		if rt.Present(name) {
			return true
		}
	}
	return false
}

func userCheckFields(m map[string]any) [][2]string {
	return [][2]string{
		{"enabled", yesNo(asBoolVal(m["enabled"]))},
		{"exempt_domains", strings.Join(asStrings(m["exempt_domains"]), ", ")},
		{"failure_mode", asStr(m["failure_mode"])},
		{"api_key_set", yesNo(asBoolVal(m["api_key_set"]))},
	}
}

func asStrings(v any) []string {
	values := asSlice(v)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok {
			out = append(out, item)
		}
	}
	return out
}

func splitDomainList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if domain := strings.TrimSpace(part); domain != "" {
			out = append(out, domain)
		}
	}
	return out
}
