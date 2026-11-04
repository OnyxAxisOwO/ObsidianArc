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

func TestInstanceBackupCommandsAreSuperAdminOnlyAndNeverAcceptKeys(t *testing.T) {
	delegated := user.User{Role: user.RoleAdmin, AdminPermissions: []string{"settings", "security", "availability"}}
	super := user.User{Role: user.RoleSuperAdmin}
	claimed := map[string]bool{}
	for _, cmd := range allCommands {
		for _, endpoint := range cmd.Endpoints {
			if !strings.Contains(endpoint, "/api/admin/backup") {
				continue
			}
			claimed[endpoint] = true
			if cmd.Permission != "super_admin" {
				t.Errorf("%s declares permission %q, want super_admin", cmd.Name, cmd.Permission)
			}
			if hasPermission(delegated, cmd.Permission) {
				t.Errorf("delegated settings admin can access %s", cmd.Name)
			}
			if !hasPermission(super, cmd.Permission) {
				t.Errorf("super admin cannot access %s", cmd.Name)
			}
			for _, flag := range cmd.Flags {
				name := strings.ToLower(flag.Name)
				if strings.Contains(name, "access-key") || strings.Contains(name, "secret-key") || strings.Contains(name, "secret-access") {
					t.Errorf("%s accepts credential flag %s, which would remain in local terminal history", cmd.Name, flag.Name)
				}
			}
		}
	}
	for _, route := range []string{
		"GET /api/admin/backup", "PUT /api/admin/backup",
		"POST /api/admin/backup/test", "POST /api/admin/backup/run",
	} {
		if !claimed[route] {
			t.Errorf("instance backup route %q is not available from the console", route)
		}
	}
}

// backup configure sends the stored destination back with only the requested
// change. The server reads a destination field it did not receive as a blank
// one: a missing type falls back to S3, and a missing WebDAV address is either
// cleared or, while its password is held, refused as a move. The body is
// compared whole so that no field can drop out unnoticed.
func TestBackupConfigureSendsTheStoredDestinationBack(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	const webdavStored = `{"type":"webdav","enabled":true,"endpoint":"","bucket":"","region":"us-east-1","prefix":"arc",` +
		`"webdav_url":"https://dav.example.test/backups","webdav_username":"arc","webdav_secret_configured":true,` +
		`"interval_hours":24,"retention_hours":168}`
	const s3StoredWithWebDAVLeft = `{"type":"s3","enabled":true,"endpoint":"https://s3.example.test","bucket":"arc-backups",` +
		`"region":"auto","prefix":"arc","secret_configured":true,` +
		`"webdav_url":"https://dav.example.test/backups","webdav_username":"arc","webdav_secret_configured":true,` +
		`"interval_hours":24,"retention_hours":168}`
	cases := []struct {
		name    string
		current string
		line    string
		want    map[string]any
	}{
		{
			name:    "a WebDAV instance keeps its type and address",
			current: webdavStored,
			line:    "backup configure --interval-hours 12",
			want: map[string]any{
				"type": "webdav", "enabled": true, "endpoint": "", "bucket": "", "region": "us-east-1", "prefix": "arc",
				"access_key_id": "", "secret_access_key": "", "webdav_url": "https://dav.example.test/backups",
				"webdav_username": "arc", "interval_hours": 12, "retention_hours": 168,
			},
		},
		{
			name:    "an S3 instance keeps the WebDAV address it still holds",
			current: s3StoredWithWebDAVLeft,
			line:    "backup configure --interval-hours 12",
			want: map[string]any{
				"type": "s3", "enabled": true, "endpoint": "https://s3.example.test", "bucket": "arc-backups",
				"region": "auto", "prefix": "arc", "access_key_id": "", "secret_access_key": "",
				"webdav_url": "https://dav.example.test/backups", "webdav_username": "arc",
				"interval_hours": 12, "retention_hours": 168,
			},
		},
		{
			name:    "a changed endpoint is the only field that differs from the stored one",
			current: s3StoredWithWebDAVLeft,
			line:    "backup configure --endpoint https://new-s3.example.test",
			want: map[string]any{
				"type": "s3", "enabled": true, "endpoint": "https://new-s3.example.test", "bucket": "arc-backups",
				"region": "auto", "prefix": "arc", "access_key_id": "", "secret_access_key": "",
				"webdav_url": "https://dav.example.test/backups", "webdav_username": "arc",
				"interval_hours": 24, "retention_hours": 168,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var puts []any
			c := New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
				switch method + " " + path {
				case "GET /api/admin/backup":
					return Response{Status: 200, Body: []byte(tc.current)}, nil
				case "PUT /api/admin/backup":
					puts = append(puts, body)
					return Response{Status: 200, Body: []byte(`{"ok":true}`)}, nil
				default:
					return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
				}
			}})
			var out bytes.Buffer
			session := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 120}
			if result := c.Execute(context.Background(), session, &out, tc.line); !result.OK {
				t.Fatalf("%s: %+v\n%s", tc.line, result, out.String())
			}
			if len(puts) != 1 {
				t.Fatalf("PUT calls = %d, want 1", len(puts))
			}
			body, ok := puts[0].(map[string]any)
			if !ok {
				t.Fatalf("PUT body has type %T", puts[0])
			}
			if !reflect.DeepEqual(body, tc.want) {
				t.Errorf("PUT body = %#v, want %#v", body, tc.want)
			}
		})
	}
}
