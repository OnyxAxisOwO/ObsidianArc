package console

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// completionValues lists what one Tab press at the end of line offers, by the
// value that would be spliced in.
func completionValues(c *Console, s *Session, line string) []string {
	var values []string
	for _, item := range c.Complete(context.Background(), s, line, len(line)).Items {
		values = append(values, item.Value)
	}
	return values
}

func containsValue(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// A flag after a positional argument or after a flag with its value used to
// find no command, so only --help and --json were offered. The line is
// resolved the way Execute resolves it, so these complete as they run.
func TestFlagCompletionFollowsArgumentsAndValueFlags(t *testing.T) {
	c := New(Options{Dispatch: func(context.Context, user.User, string, string, any) (Response, error) {
		return Response{Status: 200, Body: []byte(`{}`)}, nil
	}})
	s := &Session{Actor: watchAdmin("desk"), Transport: "web", Lang: "en", Width: 100}

	cases := []struct {
		name string
		line string
		want string
	}{
		{"straight after the name", "user list --ro", "--role"},
		{"after a value-taking flag", "user list --q alice --ro", "--role"},
		{"after a bare flag and its value", "user list --limit 5 --ro", "--role"},
		{"after a positional argument", "user edit alice --st", "--status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completionValues(c, s, tc.line); !containsValue(got, tc.want) {
				t.Errorf("Complete(%q) = %v, want it to include %s", tc.line, got, tc.want)
			}
		})
	}
}
