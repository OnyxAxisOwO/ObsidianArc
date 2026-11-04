package server

import (
	"net/http"
	"testing"
)

func TestMovingInstanceBackupEndpointNeedsItsCredentialsEnteredAgain(t *testing.T) {
	in := newInstance(t)
	founder := in.register("backup-founder", "a-good-password")
	input := map[string]any{
		"enabled": true, "endpoint": "https://storage.example.test", "bucket": "arc-backups",
		"region": "auto", "prefix": "instance", "interval_hours": 24, "retention_days": 7,
		"access_key_id": "qa-move-access", "secret_access_key": "qa-move-secret",
	}
	if response := in.do(http.MethodPut, "/api/admin/backup", input, founder); response.Code != http.StatusOK {
		t.Fatalf("save backup: %d %s", response.Code, response.Body.String())
	}

	input["endpoint"] = "https://attacker.example.test"
	input["access_key_id"], input["secret_access_key"] = "", ""
	refused := in.do(http.MethodPut, "/api/admin/backup", input, founder)
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("moving the endpoint without credentials: %d %s", refused.Code, refused.Body.String())
	}
	envelope := decode[struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, refused)
	if envelope.Error.Code != "backup_credentials_needed" || envelope.Error.Message == "" {
		t.Fatalf("refusal = code %q message %q, want backup_credentials_needed with a message",
			envelope.Error.Code, envelope.Error.Message)
	}

	read := in.do(http.MethodGet, "/api/admin/backup", nil, founder)
	if read.Code != http.StatusOK {
		t.Fatalf("read backup after refusal: %d %s", read.Code, read.Body.String())
	}
	status := decode[struct {
		Endpoint         string `json:"endpoint"`
		SecretConfigured bool   `json:"secret_configured"`
	}](t, read)
	if status.Endpoint != "https://storage.example.test" || !status.SecretConfigured {
		t.Fatalf("refused move changed the saved storage: endpoint %q secret_configured %v", status.Endpoint, status.SecretConfigured)
	}

	input["access_key_id"], input["secret_access_key"] = "qa-move-access-2", "qa-move-secret-2"
	if response := in.do(http.MethodPut, "/api/admin/backup", input, founder); response.Code != http.StatusOK {
		t.Fatalf("move the endpoint with its credentials typed again: %d %s", response.Code, response.Body.String())
	}
}
