package database

import (
	"context"
	"testing"
)

func TestAdministratorAndActivityUpgrade(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations(db.Dialect())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version >= "0032" {
			break
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"admin", "session", "chat", "idle"} {
		role := "user"
		if id == "admin" {
			role = "admin"
		}
		exec(`INSERT INTO users (id, username, username_lower, password_hash, role, created_at, updated_at, last_login_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, id, id, "unused", role, 1, 1, 100)
	}
	exec(`INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`, "s", "session", 1, 1000, 200)
	exec(`INSERT INTO conversations (id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?)`, "c", "chat", 1, 400)
	exec(`INSERT INTO messages (id, conversation_id, user_id, seq, role, created_at) VALUES (?, ?, ?, ?, ?, ?)`, "m", "c", "chat", 1, "user", 300)
	applied, err := db.Migrate(ctx)
	if err != nil || len(applied) < 2 {
		t.Fatalf("upgrade: %v %v", applied, err)
	}
	for id, want := range map[string]int64{"admin": 100, "session": 200, "chat": 300, "idle": 100} {
		var role, permissions string
		var active int64
		if err := db.QueryRow(ctx, `SELECT role, admin_permissions, last_active_at FROM users WHERE id = ?`, id).Scan(&role, &permissions, &active); err != nil {
			t.Fatal(err)
		}
		wantRole := "user"
		if id == "admin" {
			wantRole = "super_admin"
		}
		if role != wantRole || permissions != "[]" || active != want {
			t.Errorf("%s upgraded to %s/%s/%d", id, role, permissions, active)
		}
	}
	// A restart must not promote a newly delegated administrator.
	exec(`UPDATE users SET role = ? WHERE id = ?`, "admin", "session")
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := db.QueryRow(ctx, `SELECT role FROM users WHERE id = ?`, "session").Scan(&role); err != nil || role != "admin" {
		t.Fatalf("restart role: %s %v", role, err)
	}
}

func TestRequestOverrideUpgradePreservesGroupAccess(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	if err := db.ensureMigrationsTable(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations(db.Dialect())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version >= "0034" {
			break
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO providers(id,name,kind,base_url,api_key_enc,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, "p", "test", "openai", "https://example.com", []byte("test"), 1, 1)
	for _, model := range []string{"none", "view", "use"} {
		exec(`INSERT INTO models (id,provider_id,model_id,display_name,created_at,updated_at) VALUES (?,?,?,?,?,?)`, model, "p", model, model, 1, 1)
	}
	for _, group := range []string{"open", "restricted"} {
		exec(`INSERT INTO user_groups(id,name,allow_all_models,created_at,updated_at) VALUES (?,?,?,?,?)`, group, group, group == "open", 1, 1)
		for _, access := range []string{"view", "use"} {
			exec(`INSERT INTO group_models (group_id,model_id,access) VALUES (?,?,?)`, group, access, access)
		}
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"open", "restricted"} {
		for _, want := range []string{"none", "view", "use"} {
			var got string
			err := db.QueryRow(ctx, `SELECT access FROM group_models WHERE group_id = ? AND model_id = ?`, group, want).Scan(&got)
			if want == "none" && IsNotFound(err) {
				continue
			}
			if err != nil || got != want {
				t.Fatalf("%s/%s: %q %v", group, want, got, err)
			}
		}
	}
}
