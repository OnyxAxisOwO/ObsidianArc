package plugin

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
)

// A backend that cannot answer must not take the invitee list down with it:
// the list is the inviter's own and a plugin only adds to it. The decorator
// reports the failure, and the invite list skips a decorator that fails and
// keeps its rows, logging the failure once. The guard is different, since it
// decides who gets in, so it refuses instead.
func TestAnInviteeDecoratorThatCannotAnswerReportsItsFailure(t *testing.T) {
	m := &Manager{}
	// No backend: the call fails as it does when a backend is down.
	l := &loaded{name: "demo"}
	invitees := []invite.Invitee{{UserID: "u1", Entry: map[string]any{"name": "a"}}}

	got, err := m.decorator(l)(context.Background(), "inviter", invitees)
	if err == nil {
		t.Fatal("a decorator whose backend is down reported no failure, so the invite list would not log it")
	}
	if len(got) != 0 {
		t.Fatalf("a failed decorator returned rows: %+v", got)
	}
}
