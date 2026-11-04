package plugin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// rigOn is newRig over the database cfg names, for a test whose subject is the
// engine: the same test runs on SQLite, and on PostgreSQL when CI names one.
func rigOn(t *testing.T, cfg config.Database) *rig {
	t.Helper()
	isolate(t)
	defineGadget()
	ctx := context.Background()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return reload(t, db)
}

// A backend's transaction takes the instance row when it begins. If it waited
// for its first write, a read-only transaction would hold nothing. On SQLite the
// immediate transaction already holds the file's write lock, so this passes
// there either way; on PostgreSQL the row is what another change waits on.
func TestABackendTransactionHoldsTheInstanceLockFromItsStart(t *testing.T) {
	r := rigOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "plugins.db")))
	man := &arcx.Manifest{Permissions: []string{arcx.PermDB}}
	host := r.manager.hostFunc(&loaded{name: "ledger", pkg: &arcx.Package{Manifest: man}})
	call := &wasm.Call{Ctx: context.Background()}

	if _, err := host(call, "db.begin", nil); err != nil {
		t.Fatal(err)
	}
	taken := make(chan error, 1)
	go func() {
		taken <- r.db.Tx(context.Background(), func(tx *database.Tx) error {
			return settings.Lock(context.Background(), tx)
		})
	}()
	select {
	case err := <-taken:
		t.Fatalf("the instance lock was free while the backend's transaction was open: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := host(call, "db.rollback", nil); err != nil {
		t.Fatal(err)
	}
	if err := <-taken; err != nil {
		t.Fatalf("the instance lock after the transaction ended: %v", err)
	}
}

// The deadlock the instance lock prevents needs PostgreSQL. The administrator's
// side takes the instance row and then an account; the backend's side takes an
// account and then needs the instance row. Taken in opposite orders, each waits
// for the other, and the server aborts one of them.
func TestAPluginTransactionAndAnAdministratorsChangeDoNotDeadlock(t *testing.T) {
	r := rigOn(t, dbtest.Postgres(t, "a plugin transaction and an administrator's change deadlock only on PostgreSQL"))
	ctx := context.Background()
	first := accountOf(t, r, "first", user.RoleUser)
	second := accountOf(t, r, "second", user.RoleUser)
	man := &arcx.Manifest{Permissions: []string{arcx.PermDB, arcx.PermUsers}}
	host := r.manager.hostFunc(&loaded{name: "ledger", pkg: &arcx.Package{Manifest: man}})
	call := &wasm.Call{Ctx: ctx}
	raw := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	if _, err := host(call, "db.begin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host(call, "db.exec", raw(map[string]any{
		"sql": "UPDATE users SET updated_at = updated_at WHERE id = ?", "args": []string{first.ID}, "tx": true,
	})); err != nil {
		t.Fatal(err)
	}

	administrator := make(chan error, 1)
	go func() {
		administrator <- r.db.Tx(ctx, func(tx *database.Tx) error {
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, first.ID)
			return err
		})
	}()
	time.Sleep(300 * time.Millisecond)

	if _, err := host(call, "users.set_status", raw(map[string]any{
		"id": second.ID, "status": "disabled", "tx": true,
	})); err != nil {
		t.Fatalf("the backend's account change inside its transaction: %v", err)
	}
	if _, err := host(call, "db.commit", nil); err != nil {
		t.Fatal(err)
	}
	if err := <-administrator; err != nil {
		t.Fatalf("the administrator's change: %v", err)
	}
}

// While a backend's transaction is open, a statement that is not part of it
// would take a second connection and wait on the instance row the transaction
// holds, for as long as the call allows. It is refused at once; the same
// statement goes ahead once the transaction has ended.
func TestAStatementOutsideAnOpenBackendTransactionIsRefused(t *testing.T) {
	r := rigOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "plugins.db")))
	man := &arcx.Manifest{Permissions: []string{arcx.PermDB, arcx.PermUsers}}
	host := r.manager.hostFunc(&loaded{name: "ledger", pkg: &arcx.Package{Manifest: man}})
	call := &wasm.Call{Ctx: context.Background()}
	outside := json.RawMessage(`{"sql":"SELECT 1","tx":false}`)
	inside := json.RawMessage(`{"sql":"SELECT 1","tx":true}`)
	setStatus := json.RawMessage(`{"id":"01NOBODY","status":"disabled","tx":false}`)

	if _, err := host(call, "db.begin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host(call, "db.query", outside); hostCode(err) != "tx_open" {
		t.Fatalf("a query outside the transaction: %v", err)
	}
	if _, err := host(call, "users.set_status", setStatus); hostCode(err) != "tx_open" {
		t.Fatalf("an account change outside the transaction: %v", err)
	}
	if _, err := host(call, "db.query", inside); err != nil {
		t.Fatalf("a query inside the transaction: %v", err)
	}
	if _, err := host(call, "db.rollback", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host(call, "db.query", outside); err != nil {
		t.Fatalf("a query after the transaction ended: %v", err)
	}
}
