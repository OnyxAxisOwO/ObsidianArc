package console

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestMailAndUserCheckCommands(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	type call struct {
		method string
		path   string
		body   any
	}
	var calls []call
	var audit []AuditRecord
	c := New(Options{
		Audit: func(_ context.Context, record AuditRecord) { audit = append(audit, record) },
		Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
			calls = append(calls, call{method: method, path: path, body: body})
			switch method + " " + path {
			case "GET /api/admin/mail":
				return Response{Status: 200, Body: []byte(`{"host":"smtp.example.com","port":587,"username":"arc","from":"arc@example.com","implicit_tls":false,"public_url":"https://arc.example.com","password_set":true}`)}, nil
			case "PUT /api/admin/mail":
				return Response{Status: 200, Body: []byte(`{"host":"smtp.new.example.com","port":587,"username":"arc","from":"arc@example.com","implicit_tls":false,"public_url":"https://arc.example.com","password_set":true}`)}, nil
			case "POST /api/admin/mail/test":
				return Response{Status: 200, Body: []byte(`{"sent":true}`)}, nil
			case "GET /api/admin/usercheck":
				return Response{Status: 200, Body: []byte(`{"enabled":true,"exempt_domains":["example.com"],"failure_mode":"reject","api_key_set":true}`)}, nil
			case "PUT /api/admin/usercheck":
				return Response{Status: 200, Body: []byte(`{"enabled":false,"exempt_domains":["example.com","example.net"],"failure_mode":"reject","api_key_set":true}`)}, nil
			case "POST /api/admin/usercheck/test":
				return Response{Status: 200, Body: []byte(`{"disposable":true,"skipped":false}`)}, nil
			default:
				return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
			}
		},
	})
	run := func(line string) (Result, string) {
		var out bytes.Buffer
		session := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 120}
		return c.Execute(context.Background(), session, &out, line), out.String()
	}

	t.Run("show displays only safe mail settings", func(t *testing.T) {
		calls = nil
		result, out := run("mail show")
		if !result.OK || len(calls) != 1 || calls[0].path != "/api/admin/mail" {
			t.Fatalf("result %+v, calls %+v, output:\n%s", result, calls, out)
		}
		if !strings.Contains(out, "password_set") || strings.Contains(out, "password\t") {
			t.Fatalf("expected password status without a password value:\n%s", out)
		}
	})

	t.Run("set changes one field while keeping omitted values and stored password", func(t *testing.T) {
		calls = nil
		result, out := run("mail set --host smtp.new.example.com")
		if !result.OK || len(calls) != 2 || calls[0].method != "GET" || calls[1].method != "PUT" {
			t.Fatalf("result %+v, calls %+v, output:\n%s", result, calls, out)
		}
		body, ok := calls[1].body.(map[string]any)
		if !ok {
			t.Fatalf("PUT body has type %T", calls[1].body)
		}
		want := map[string]any{
			"host": "smtp.new.example.com", "port": 587, "username": "arc", "from": "arc@example.com",
			"implicit_tls": false, "public_url": "https://arc.example.com", "password": "", "clear_password": false,
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("PUT body = %#v, want %#v", body, want)
		}
	})

	t.Run("set can replace a password without exposing it in audit", func(t *testing.T) {
		calls, audit = nil, nil
		result, out := run("mail set --password one-time-secret")
		if !result.OK || len(calls) != 2 || len(audit) != 1 {
			t.Fatalf("result %+v, calls %+v, audit %+v, output:\n%s", result, calls, audit, out)
		}
		body := calls[1].body.(map[string]any)
		if body["password"] != "one-time-secret" || body["clear_password"] != false {
			t.Errorf("password update body = %#v", body)
		}
		if strings.Contains(audit[0].Line, "one-time-secret") || !strings.Contains(audit[0].Line, "***") {
			t.Errorf("sensitive flag not masked in audit: %q", audit[0].Line)
		}
	})

	t.Run("test mail sends the requested recipient", func(t *testing.T) {
		calls = nil
		result, out := run("mail test operator@example.com")
		if !result.OK || len(calls) != 1 || calls[0].path != "/api/admin/mail/test" {
			t.Fatalf("result %+v, calls %+v, output:\n%s", result, calls, out)
		}
		if body, ok := calls[0].body.(map[string]any); !ok || body["to"] != "operator@example.com" {
			t.Errorf("mail test body = %#v", calls[0].body)
		}
	})

	t.Run("set usercheck preserves omitted policy and parses domain list", func(t *testing.T) {
		calls = nil
		result, out := run(`usercheck set --enabled false --exempt-domains "example.com, example.net"`)
		if !result.OK || len(calls) != 2 || calls[0].method != "GET" || calls[1].method != "PUT" {
			t.Fatalf("result %+v, calls %+v, output:\n%s", result, calls, out)
		}
		body, ok := calls[1].body.(map[string]any)
		if !ok {
			t.Fatalf("PUT body has type %T", calls[1].body)
		}
		wantDomains := []string{"example.com", "example.net"}
		if body["enabled"] != false || body["failure_mode"] != "reject" || body["api_key"] != "" || body["clear_api_key"] != false || !reflect.DeepEqual(body["exempt_domains"], wantDomains) {
			t.Errorf("PUT body = %#v", body)
		}
	})

	t.Run("show displays usercheck policy without exposing its key", func(t *testing.T) {
		calls = nil
		result, out := run("usercheck show")
		if !result.OK || len(calls) != 1 || calls[0].path != "/api/admin/usercheck" {
			t.Fatalf("result %+v, calls %+v, output:\n%s", result, calls, out)
		}
		if !strings.Contains(out, "api_key_set") || strings.Contains(out, "api_key\t") {
			t.Fatalf("expected key status without a key value:\n%s", out)
		}
	})

	t.Run("set can replace the UserCheck key without exposing it in audit", func(t *testing.T) {
		calls, audit = nil, nil
		result, out := run("usercheck set --api-key hidden-usercheck-key")
		if !result.OK || len(calls) != 2 || len(audit) != 1 {
			t.Fatalf("result %+v, calls %+v, audit %+v, output:\n%s", result, calls, audit, out)
		}
		body := calls[1].body.(map[string]any)
		if body["api_key"] != "hidden-usercheck-key" || body["clear_api_key"] != false {
			t.Errorf("API key update body = %#v", body)
		}
		if strings.Contains(audit[0].Line, "hidden-usercheck-key") || !strings.Contains(audit[0].Line, "***") {
			t.Errorf("sensitive flag not masked in audit: %q", audit[0].Line)
		}
	})

	t.Run("test usercheck sends the requested email", func(t *testing.T) {
		calls = nil
		result, out := run("usercheck test person@example.com")
		if !result.OK || len(calls) != 1 || calls[0].path != "/api/admin/usercheck/test" {
			t.Fatalf("result %+v, calls %+v, output:\n%s", result, calls, out)
		}
		if body, ok := calls[0].body.(map[string]any); !ok || body["email"] != "person@example.com" {
			t.Errorf("UserCheck test body = %#v", calls[0].body)
		}
	})
}

func TestMeVerifySupportsCodeAndResend(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "tester", Role: user.RoleUser}
	type call struct {
		method string
		path   string
		body   any
	}
	var calls []call
	var audit []AuditRecord
	c := New(Options{
		Audit: func(_ context.Context, record AuditRecord) { audit = append(audit, record) },
		Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
			calls = append(calls, call{method: method, path: path, body: body})
			return Response{Status: 204}, nil
		},
	})
	run := func(line string) (Result, string) {
		var out bytes.Buffer
		session := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 120}
		return c.Execute(context.Background(), session, &out, line), out.String()
	}

	result, out := run("me verify --code 123456")
	if !result.OK || len(calls) != 1 || calls[0].method != "POST" || calls[0].path != "/api/profile/verify/code" {
		t.Fatalf("code verification: result %+v, calls %+v, output:\n%s", result, calls, out)
	}
	if body, ok := calls[0].body.(map[string]any); !ok || body["code"] != "123456" {
		t.Fatalf("verification body = %#v", calls[0].body)
	}
	if len(audit) != 1 || strings.Contains(audit[0].Line, "123456") || !strings.Contains(audit[0].Line, "***") {
		t.Errorf("verification code not masked in audit: %+v", audit)
	}

	calls = nil
	result, out = run("me verify")
	if !result.OK || len(calls) != 1 || calls[0].method != "POST" || calls[0].path != "/api/profile/verify/resend" || calls[0].body != nil {
		t.Fatalf("resend: result %+v, calls %+v, output:\n%s", result, calls, out)
	}
}
