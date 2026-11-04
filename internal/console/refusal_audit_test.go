package console

import (
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A refused command is still an attempt the security trail should show: who
// tried it, and why it was turned away. The rest of the line stays out of the
// record. A refused line was never parsed, so nothing has yet said which of
// its tokens is a secret, and a password typed where a flag was meant is one
// of the usual reasons a line is refused.
func TestRefusedCommandsAreRecordedWithoutTheirArguments(t *testing.T) {
	const secret = "refused-S3cret-Passw0rd"

	refused := func(t *testing.T, rig *auditRig) AuditRecord {
		t.Helper()
		if len(rig.audit) != 1 {
			t.Fatalf("want one audit record for the refused line, got %d: %+v", len(rig.audit), rig.audit)
		}
		record := rig.audit[0]
		if strings.Contains(record.Line, secret) {
			t.Errorf("the refused line's argument reached the audit record: %q", record.Line)
		}
		if record.OK {
			t.Errorf("a refused line was recorded as OK: %+v", record)
		}
		return record
	}

	t.Run("permission denied", func(t *testing.T) {
		rig := newAuditRig(t, nil)
		delegate := user.User{ID: id.New(), Username: "delegate", Role: user.RoleAdmin}
		result, _ := runLine(rig.console, delegate, "user create svc-bot "+secret, false)
		if result.Code != "permission_denied" {
			t.Fatalf("result = %+v, want permission_denied", result)
		}
		if record := refused(t, rig); record.Line != "user create" || record.Code != "permission_denied" {
			t.Errorf("record = %+v, want the command name alone with permission_denied", record)
		}
	})

	t.Run("flag without its value", func(t *testing.T) {
		rig := newAuditRig(t, nil)
		result, _ := rig.run("user create svc-bot " + secret + " --role")
		if result.Code != "parse_error" {
			t.Fatalf("result = %+v, want parse_error", result)
		}
		if record := refused(t, rig); record.Line != "user create" || record.Code != "parse_error" {
			t.Errorf("record = %+v, want the command name alone with parse_error", record)
		}
	})

	t.Run("destructive without yes", func(t *testing.T) {
		rig := newAuditRig(t, nil)
		result, _ := rig.run("user delete alice")
		if result.Code != "confirmation_required" {
			t.Fatalf("result = %+v, want confirmation_required", result)
		}
		if record := refused(t, rig); record.Line != "user delete alice" || record.Code != "confirmation_required" {
			t.Errorf("record = %+v, want the masked line with confirmation_required", record)
		}
	})

	t.Run("line that does not tokenize", func(t *testing.T) {
		rig := newAuditRig(t, nil)
		result, _ := rig.run(`user create "svc-bot ` + secret)
		if result.Code != "parse_error" {
			t.Fatalf("result = %+v, want parse_error", result)
		}
		if record := refused(t, rig); record.Code != "parse_error" {
			t.Errorf("record = %+v, want parse_error", record)
		}
	})
}
