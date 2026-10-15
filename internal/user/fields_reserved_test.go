package user

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// A field is written by an account's own profile edit, so its name must not be
// one of the users table's. The list that guards this is written by hand, which
// is how a column added to the table later gets forgotten: this holds it to the
// schema the migrations actually build.
func TestEveryColumnOfTheUsersTableIsReservedFromPlugins(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "columns.db"), MaxOpenConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT * FROM users LIMIT 0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) == 0 {
		t.Fatalf("the users table's columns: %v %v", columns, err)
	}

	s := storeWithGate(map[string]bool{"zeta": true})
	for _, column := range columns {
		if !isCoreColumn(column) {
			t.Errorf("users.%s is not reserved: a plugin could name a field after it", column)
		}
		// The same answer the installer gives, whichever way a field arrives.
		if err := s.CheckPluginFields("zeta", []Field{{Key: column}}); err == nil {
			t.Errorf("a plugin field called %s was accepted", column)
		}
	}
}

func TestAPluginCannotNameAFieldAfterALoginOrCounterColumn(t *testing.T) {
	s := storeWithGate(map[string]bool{"zeta": true})
	for _, key := range []string{"password_hash", "username_lower", "email_lower", "invite_reward_count", "signup_rate_key"} {
		if err := s.AddPluginFields("zeta", []Field{{Key: key}}); err == nil {
			t.Errorf("%s was accepted as a plugin field", key)
		}
		if s.HasField(key) {
			t.Errorf("%s is a field after being refused", key)
		}
	}
}

// A field with no owner reads as core and is selected on every account query
// even while its migration has not run, so the registry refuses it outright.
func TestDefineFieldRefusesAFieldWithNoOwner(t *testing.T) {
	// An earlier test in this binary may have frozen the registry by reading
	// the accounts table; the owner check is the thing under test here.
	prev := fieldsFrozen.Load()
	fieldsFrozen.Store(false)
	defer fieldsFrozen.Store(prev)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a field with no owning plugin was defined")
		}
		if !strings.Contains(fmt.Sprint(r), "no owning plugin") {
			t.Fatalf("panic = %v, want the owner check", r)
		}
	}()
	DefineField(Field{Key: "orphan_field"})
}
