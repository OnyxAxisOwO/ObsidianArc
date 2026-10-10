package plugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// A backend that talks to the ABI directly, without the SDK's own refusal, must
// not be able to check a human challenge while a transaction is open: the check
// may ask a service elsewhere, and the open transaction would hold a connection
// for as long as that takes. Once the transaction ends the same check goes ahead.
func TestAChallengeIsNotCheckedInsideATransaction(t *testing.T) {
	r := newRig(t)
	calls := 0
	r.manager.host = &Host{Challenge: Challenge{
		Describe: func() ChallengeKinds { return ChallengeKinds{PoW: true} },
		Verify: func(context.Context, ChallengeProof, string) error {
			calls++
			return nil
		},
	}}
	man := &arcx.Manifest{Permissions: []string{arcx.PermDB, arcx.PermChallenge}}
	host := r.manager.hostFunc(&loaded{name: "gate", pkg: &arcx.Package{Manifest: man}})
	call := &wasm.Call{Ctx: context.Background()}
	proof := json.RawMessage(`{"turnstile":"token","ip":"203.0.113.7"}`)

	if _, err := host(call, "db.begin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host(call, "challenge.verify", proof); hostCode(err) != "tx_open" {
		t.Fatalf("a check inside a transaction: %v", err)
	}
	if calls != 0 {
		t.Fatal("the service was asked inside a transaction")
	}
	if _, err := host(call, "db.rollback", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host(call, "challenge.verify", proof); err != nil {
		t.Fatalf("a check after the transaction ended: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the service was asked %d times, want once", calls)
	}
}
