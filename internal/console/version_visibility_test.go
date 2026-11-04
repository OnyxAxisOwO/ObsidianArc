package console

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// about.show_software_info hides the build from members on /api/health. The
// console answered around it: the version command and the spec served the
// build string to every signed-in account.
func TestTheBuildVersionFollowsTheShowSoftwareInfoSetting(t *testing.T) {
	const build = "v9.9.9-test"
	member := user.User{ID: id.New(), Username: "member", Role: user.RoleUser, Status: user.StatusActive}
	admin := watchAdmin("desk")
	noDispatch := func(context.Context, user.User, string, string, any) (Response, error) {
		return Response{Status: 200, Body: []byte(`{}`)}, nil
	}

	t.Run("hidden from a member", func(t *testing.T) {
		c := New(Options{Dispatch: noDispatch, Version: build, ShowVersion: func() bool { return false }})
		session := &Session{Actor: member, Transport: "web", Lang: "en", Width: 100}

		var out bytes.Buffer
		if result := c.Execute(context.Background(), session, &out, "version"); result.OK {
			t.Errorf("version printed the build to a member:\n%s", out.String())
		}
		if strings.Contains(out.String(), build) {
			t.Errorf("the build reached a member: %q", out.String())
		}
		if got := c.Spec(session).Version; got != "" {
			t.Errorf("the spec carried the build %q to a member", got)
		}
	})

	t.Run("an administrator still sees it", func(t *testing.T) {
		c := New(Options{Dispatch: noDispatch, Version: build, ShowVersion: func() bool { return false }})
		session := &Session{Actor: admin, Transport: "web", Lang: "en", Width: 100}

		var out bytes.Buffer
		if result := c.Execute(context.Background(), session, &out, "version"); !result.OK {
			t.Fatalf("version failed for an administrator: %s", out.String())
		}
		if !strings.Contains(out.String(), build) {
			t.Errorf("an administrator did not see the build: %q", out.String())
		}
		if got := c.Spec(session).Version; got != build {
			t.Errorf("spec version = %q for an administrator, want %q", got, build)
		}
	})

	t.Run("shown to a member when the setting is on or absent", func(t *testing.T) {
		session := &Session{Actor: member, Transport: "web", Lang: "en", Width: 100}
		for _, show := range []func() bool{nil, func() bool { return true }} {
			c := New(Options{Dispatch: noDispatch, Version: build, ShowVersion: show})
			var out bytes.Buffer
			if result := c.Execute(context.Background(), session, &out, "version"); !result.OK {
				t.Errorf("version failed for a member while the build is shown: %s", out.String())
			}
			if got := c.Spec(session).Version; got != build {
				t.Errorf("spec version = %q for a member while the build is shown, want %q", got, build)
			}
		}
	})
}
