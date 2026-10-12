package plugin

import (
	"errors"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// A guard that answers anything other than allow, restrict or refuse must not
// let the visitor through: the zero Verdict is unrestricted, so falling
// through to it is an open door for a backend that spells its answer another
// way.
func TestOnlyAnExplicitAllowLetsAGuardsVisitorThrough(t *testing.T) {
	for _, answer := range []guardAnswer{{}, {Verdict: "deny"}, {Verdict: "ALLOW"}} {
		v, err := answer.verdict()
		var refusal *auth.GuardRefusal
		if !errors.As(err, &refusal) {
			t.Errorf("answer %+v let the visitor through: %+v, %v", answer, v, err)
		}
	}
	if _, err := (guardAnswer{Verdict: "allow"}).verdict(); err != nil {
		t.Errorf("an explicit allow was refused: %v", err)
	}
	if v, err := (guardAnswer{Verdict: "restrict", Reason: "r"}).verdict(); err != nil || !v.Restrict {
		t.Errorf("restrict: %+v, %v", v, err)
	}
	var refusal *auth.GuardRefusal
	if _, err := (guardAnswer{Verdict: "refuse", Status: 429, Code: "slow", Message: "wait"}).verdict(); !errors.As(err, &refusal) {
		t.Errorf("refuse was not a refusal: %v", err)
	}
}

// A command with no permission named used to run for every administrator.
// The backend runs with the package's own host permissions, so it defaults to
// the widest grant a terminal can hold.
func TestAConsoleCommandWithoutAGrantNeedsSuperAdmin(t *testing.T) {
	l := &loaded{name: "demo", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Console: []arcx.ConsoleCommand{{Name: "demo ping", Summary: arcx.Text{EN: "Ping"}}},
	}}}
	cmds := (&Manager{}).commandsOf(l)
	if len(cmds) != 1 || cmds[0].Permission != "super_admin" {
		t.Fatalf("permission = %+v; want super_admin", cmds)
	}
}

// A backend's own failure text can name a credential it was handed. The
// console must not draw it; the log may keep it.
func TestAnInternalCommandFailureIsNotShownToTheTerminal(t *testing.T) {
	const secret = "sk-live-12345"
	err := commandFailure(&wasm.GuestError{Code: "internal", Message: "upstream rejected key " + secret, Status: 500, Internal: true}, nil)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("the console drew the backend's failure: %v", err)
	}
	err = commandFailure(&wasm.GuestError{Code: "not_found", Message: "no such thing", Status: 404}, nil)
	if err == nil || err.Error() != "no such thing" {
		t.Fatalf("a deliberate refusal was not drawn as written: %v", err)
	}
}
