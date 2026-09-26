package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
)

func TestMailConfigurationResponseNeverContainsPassword(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "mail-admin.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const password = "do-not-return-this-smtp-password"
	legacy := mail.Config{
		Host: "smtp.example.com", Port: 587, Username: "mailer@example.com",
		Password: password, PublicURL: "https://example.com",
	}
	manager, err := mail.NewManager(ctx, db, mail.New(legacy), legacy, []byte("test-instance-secret"))
	if err != nil {
		t.Fatalf("new mail manager: %v", err)
	}

	response := httptest.NewRecorder()
	if err := (&Handlers{Mail: manager}).getMail(response, httptest.NewRequest("GET", "/api/admin/mail", nil)); err != nil {
		t.Fatalf("get mail config: %v", err)
	}
	if response.Code != 200 {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantKeys := []string{"host", "port", "username", "from", "implicit_tls", "public_url", "password_set"}
	gotKeys := make([]string, 0, len(body))
	for key := range body {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	sort.Strings(wantKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("response keys = %v, want %v", gotKeys, wantKeys)
	}
	if _, ok := body["password"]; ok {
		t.Fatal("mail GET exposed a password field")
	}
	if response.Body.String() == "" || strings.Contains(response.Body.String(), password) {
		t.Fatal("mail GET exposed the configured SMTP password")
	}
	if body["password_set"] != true {
		t.Fatalf("password_set = %v, want true", body["password_set"])
	}
}
