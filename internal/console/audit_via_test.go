package console

import (
	"bytes"
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/audit"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A command reaches the admin routes in process, so without a mark the audit
// trail would file it as a click in the backoffice. Each transport marks its
// own calls, which is what lets the trail say a change was typed over SSH.
func TestACommandsCallsAreMarkedWithItsTransport(t *testing.T) {
	for _, transport := range []string{"web", "ssh"} {
		var seen []string
		c := New(Options{Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (Response, error) {
			seen = append(seen, audit.Via(ctx))
			return Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		}})
		actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin, Status: user.StatusActive}
		var out bytes.Buffer
		result := c.Execute(context.Background(), &Session{Actor: actor, Transport: transport, Lang: "en", Width: 100}, &out, "user list")
		if !result.OK {
			t.Fatalf("user list over %s failed: %q", transport, out.String())
		}
		if len(seen) == 0 {
			t.Fatalf("user list over %s made no call", transport)
		}
		for _, via := range seen {
			if via != "console:"+transport {
				t.Errorf("a call over %s was marked %q, want console:%s", transport, via, transport)
			}
		}
	}
}
