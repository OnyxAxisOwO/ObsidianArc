package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
)

// A Manager with no host has no session store, inbox, security log or card
// store behind it. Each of these calls must say so, not dereference the nil
// service: a call that answered success would tell the package its work was
// done when it was not.
func TestAHostCallWithNoServiceBehindItIsRefusedNotDereferenced(t *testing.T) {
	r := newRig(t)
	if r.manager.host != nil {
		t.Fatal("the test needs a manager with no host")
	}
	man := &arcx.Manifest{Permissions: []string{arcx.PermSessions, arcx.PermNotify, arcx.PermSecurityLog, arcx.PermCards}}
	host := r.manager.hostFunc(&loaded{name: "riskctl", pkg: &arcx.Package{Manifest: man}})
	call := &wasm.Call{Ctx: context.Background()}

	for op, raw := range map[string]string{
		"sessions.revoke_user":   `{"user_id":"u"}`,
		"notify.push":            `{"user_id":"u","kind":"k"}`,
		"security.record":        `{"event":"x"}`,
		"cards.revoke_available": `{"user_id":"u","count":1}`,
	} {
		if _, err := host(call, op, json.RawMessage(raw)); hostCode(err) != "unavailable" {
			t.Errorf("%s with no host: %v", op, err)
		}
	}
}

// A refusal filed under one of the server's own event names would read as the
// server's entry, so the name is filed under the package's instead. A name the
// server does not write is the package's to choose and is kept as written.
func TestAGuardsRefusalIsNotFiledUnderAnEventTheServerWrites(t *testing.T) {
	r := newRig(t)
	man := &arcx.Manifest{Guards: []arcx.Guard{
		{Action: "register", Name: "gate", Event: "pow_challenge"},
		{Action: "register", Name: "other", Event: "risk_block"},
	}}
	guards := r.manager.guardsOf(&loaded{name: "riskctl", pkg: &arcx.Package{Manifest: man}})["register"]
	if len(guards) != 2 {
		t.Fatalf("guards = %d, want 2", len(guards))
	}
	if guards[0].Event != "plugin:riskctl:pow_challenge" {
		t.Errorf("a core event name was kept: %q", guards[0].Event)
	}
	if guards[1].Event != "risk_block" {
		t.Errorf("a package's own event was changed: %q", guards[1].Event)
	}
}

// A package's reason is cut to 500 characters, and a reason of Chinese text cut
// by byte would end in a half character that the store turns into a replacement
// mark. The stored reason must be whole characters, 500 of them.
func TestAPackagesLongReasonIsCutOnACharacterBoundary(t *testing.T) {
	r := newRig(t)
	r.manager.host = &Host{Security: securityevents.NewStore(r.db)}
	ctx := context.Background()
	host := r.manager.hostFunc(&loaded{name: "riskctl", pkg: &arcx.Package{Manifest: &arcx.Manifest{Permissions: []string{arcx.PermSecurityLog}}}})
	raw, _ := json.Marshal(map[string]any{"event": "long_reason", "reason": strings.Repeat("汉", 600)})
	if _, err := host(&wasm.Call{Ctx: ctx}, "security.record", raw); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := r.db.QueryRow(ctx, `SELECT reason FROM security_events WHERE event = ?`, "long_reason").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(got, utf8.RuneError) || utf8.RuneCountInString(got) != 500 {
		t.Fatalf("the stored reason is %d characters, replacement mark present: %t; want 500 whole characters",
			utf8.RuneCountInString(got), strings.ContainsRune(got, utf8.RuneError))
	}
}
