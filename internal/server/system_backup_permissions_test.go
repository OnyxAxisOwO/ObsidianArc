package server

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestInstanceBackupRequiresSuperAdministrator(t *testing.T) {
	in := newInstance(t)
	founder := in.register("backup-founder", "a-good-password")
	operator := in.register("backup-operator", "a-good-password")
	grant := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"settings"}}, founder)
	if grant.Code != http.StatusOK {
		t.Fatalf("grant settings: %d %s", grant.Code, grant.Body.String())
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/backup"},
		{http.MethodPut, "/api/admin/backup"},
		{http.MethodPost, "/api/admin/backup/test"},
		{http.MethodPost, "/api/admin/backup/run"},
	} {
		response := in.do(route.method, route.path, nil, operator)
		if response.Code != http.StatusForbidden {
			t.Errorf("delegated settings administrator accessed %s %s: %d %s",
				route.method, route.path, response.Code, response.Body.String())
		}
	}
	response := in.do(http.MethodGet, "/api/admin/backup", nil, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("super administrator cannot read backup: %d %s", response.Code, response.Body.String())
	}
}

func TestInstanceBackupCredentialsStayWriteOnlyAndSurviveSettingsEdit(t *testing.T) {
	in := newInstance(t)
	founder := in.register("backup-founder", "a-good-password")
	const access = "qa-backup-access-marker"
	const secret = "qa-backup-secret-marker"
	input := map[string]any{
		"enabled": true, "endpoint": "https://storage.example.test", "bucket": "arc-backups",
		"region": "auto", "prefix": "instance", "interval_hours": 24, "retention_days": 7,
		"access_key_id": access, "secret_access_key": secret,
	}
	response := in.do(http.MethodPut, "/api/admin/backup", input, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("save backup: %d %s", response.Code, response.Body.String())
	}
	var sealedAccess, sealedSecret []byte
	if err := in.db.QueryRow(context.Background(),
		`SELECT access_key_id_enc, secret_access_key_enc FROM system_backups WHERE id = ?`, "instance").Scan(&sealedAccess, &sealedSecret); err != nil {
		t.Fatal(err)
	}
	if len(sealedAccess) == 0 || len(sealedSecret) == 0 || bytes.Contains(sealedAccess, []byte(access)) || bytes.Contains(sealedSecret, []byte(secret)) {
		t.Fatal("storage credentials were not sealed at rest")
	}
	input["access_key_id"], input["secret_access_key"] = "", ""
	input["retention_days"] = 14
	response = in.do(http.MethodPut, "/api/admin/backup", input, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("edit retention without resending credentials: %d %s", response.Code, response.Body.String())
	}
	response = in.do(http.MethodGet, "/api/admin/backup", nil, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("read backup: %d %s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{access, secret, `"access_key_id"`, `"secret_access_key"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatal("backup status returned write-only credential data")
		}
	}
	status := decode[struct {
		Configured       bool `json:"configured"`
		SecretConfigured bool `json:"secret_configured"`
		RetentionHours   int  `json:"retention_hours"`
		RetentionDays    int  `json:"retention_days"`
	}](t, response)
	if !status.Configured || !status.SecretConfigured || status.RetentionDays != 14 || status.RetentionHours != 336 {
		t.Fatalf("saved status = %+v", status)
	}

	// Directly setting retention_hours in hours
	delete(input, "retention_days")
	input["retention_hours"] = 48
	response = in.do(http.MethodPut, "/api/admin/backup", input, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("edit retention hours: %d %s", response.Code, response.Body.String())
	}
	response = in.do(http.MethodGet, "/api/admin/backup", nil, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("read backup: %d %s", response.Code, response.Body.String())
	}
	status = decode[struct {
		Configured       bool `json:"configured"`
		SecretConfigured bool `json:"secret_configured"`
		RetentionHours   int  `json:"retention_hours"`
		RetentionDays    int  `json:"retention_days"`
	}](t, response)
	if status.RetentionHours != 48 || status.RetentionDays != 2 {
		t.Fatalf("saved retention hours = %+v, want hours=48 days=2", status)
	}
	var afterAccess, afterSecret []byte
	if err := in.db.QueryRow(context.Background(),
		`SELECT access_key_id_enc, secret_access_key_enc FROM system_backups WHERE id = ?`, "instance").Scan(&afterAccess, &afterSecret); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sealedAccess, afterAccess) || !bytes.Equal(sealedSecret, afterSecret) {
		t.Fatal("editing retention replaced the saved credentials")
	}
}
