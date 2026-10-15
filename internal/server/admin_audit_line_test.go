package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// The process log's line for an account change names the role it set. Logged
// as the request's own pointer, the text handler printed an address there, so
// the line an operator reads did not say what changed.
func TestAccountChangeAuditLineNamesTheRole(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	if res := in.do(http.MethodPatch, "/api/admin/users/"+member.userID, map[string]any{"role": "admin"}, founder); res.Code != http.StatusOK {
		t.Fatalf("promote: %d %s", res.Code, res.Body.String())
	}
	out := logged.String()
	if !strings.Contains(out, "role=admin") {
		t.Fatalf("the audit line does not name the role it set: %q", out)
	}
	if strings.Contains(out, "role=0x") {
		t.Fatalf("the audit line printed the role's address: %q", out)
	}
}
