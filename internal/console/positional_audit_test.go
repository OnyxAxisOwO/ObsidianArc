package console

import (
	"strings"
	"testing"
)

// The record keeps the command as it was typed, minus what is secret, and a
// command declares only the positionals it expects. A password typed after
// the name of `user create` is one the declaration knows nothing about, so it
// has to be masked as an undeclared token, not written as an ordinary one.
func TestAuditMasksPositionalsThatTheCommandDoesNotDeclare(t *testing.T) {
	const secret = "S3cret-Passw0rd-typed-in-the-wrong-place"
	rig := newAuditRig(t, nil)
	rig.run("user create svc-bot " + secret)
	got := rig.line(t)
	if strings.Contains(got, secret) {
		t.Errorf("the undeclared password reached the audit line: %q", got)
	}
	if want := "user create svc-bot ***"; got != want {
		t.Errorf("audit line = %q, want %q", got, want)
	}
}
