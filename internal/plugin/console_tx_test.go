package plugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// A console command reaches an admin endpoint, and some of those reach outside
// the server: a mail test, a health probe. A backend holding a transaction open
// must not run one, for the reason an outgoing request is refused: the
// transaction would stay open, with its connection and its locks, for as long
// as the call takes.
func TestAConsoleCallIsNotMadeInsideATransaction(t *testing.T) {
	r := newRig(t)
	man := &arcx.Manifest{Permissions: []string{arcx.PermDB, arcx.PermConsole}}
	host := r.manager.hostFunc(&loaded{name: "ops", pkg: &arcx.Package{Manifest: man}})
	// A console session is open for the call, but nothing is dispatched: a call
	// that got past the refusal would reach the zero runtime and fail the test.
	call := &wasm.Call{Ctx: context.Background(), State: &callState{console: &console.Runtime{}}}
	mail := json.RawMessage(`{"method":"POST","path":"/api/admin/mail/test"}`)

	if _, err := host(call, "db.begin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host(call, "console.call", mail); hostCode(err) != "tx_open" {
		t.Fatalf("a console call inside a transaction: %v", err)
	}
	if _, err := host(call, "db.rollback", nil); err != nil {
		t.Fatal(err)
	}
}
