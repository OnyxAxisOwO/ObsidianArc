package console

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// These run in real time: watch's interval has a floor of one second, so each
// test waits about one run's worth. That is the cost of asserting on a clock
// the command itself reads.

// watchRecorder is a Dispatcher that remembers which account each call was
// made as, and answers an empty user list.
type watchRecorder struct {
	mu     sync.Mutex
	actors []string
}

func (r *watchRecorder) dispatch(_ context.Context, actor user.User, _, _ string, _ any) (Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actors = append(r.actors, actor.Username)
	return Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
}

func (r *watchRecorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.actors...)
}

// watchAdmin is an administrator holding the users grant, the one `user list`
// needs.
func watchAdmin(username string) user.User {
	return user.User{ID: id.New(), Username: username, Role: user.RoleAdmin, AdminPermissions: []string{"users"}, Status: user.StatusActive}
}

// A sign-in can end while a watch is running: its session is deleted, or the
// account is disabled. The run that would follow must not happen, and the
// watch must say why.
func TestWatchStopsOnItsNextRunWhenTheSignInHasEnded(t *testing.T) {
	var (
		mu       sync.Mutex
		reauthed int
	)
	recorder := &watchRecorder{}
	c := New(Options{Dispatch: recorder.dispatch})
	session := &Session{
		Actor: watchAdmin("desk"), Transport: "web", Lang: "en", Width: 100,
		Reauthorize: func(context.Context) (user.User, error) {
			mu.Lock()
			defer mu.Unlock()
			reauthed++
			return user.User{}, errors.New("sign-in deleted")
		},
	}

	var out bytes.Buffer
	result := c.Execute(context.Background(), session, &out, "watch --interval 1s --count 5 -- user list")

	if got := recorder.calls(); len(got) != 1 {
		t.Errorf("the watch dispatched %d runs, want 1: the run after the sign-in ended must not happen", len(got))
	}
	mu.Lock()
	if reauthed != 1 {
		t.Errorf("the account was read again %d times, want 1", reauthed)
	}
	mu.Unlock()
	if result.OK {
		t.Errorf("the watch reported success after its sign-in ended:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "watch stopped") {
		t.Errorf("the stop was not explained:\n%s", out.String())
	}
}

// A run after the first is made as the account is now. Here the account has
// been renamed, which is visible to the API as the caller it is dispatched as.
func TestWatchDispatchesEachRunAsTheAccountItIsNow(t *testing.T) {
	recorder := &watchRecorder{}
	c := New(Options{Dispatch: recorder.dispatch})
	session := &Session{
		Actor: watchAdmin("before"), Transport: "web", Lang: "en", Width: 100,
		Reauthorize: func(context.Context) (user.User, error) {
			return watchAdmin("after"), nil
		},
	}

	var out bytes.Buffer
	c.Execute(context.Background(), session, &out, "watch --interval 1s --count 2 -- user list")

	got := recorder.calls()
	if len(got) != 2 || got[0] != "before" || got[1] != "after" {
		t.Errorf("dispatched as %v, want [before after]", got)
	}
	if session.Actor.Username != "after" {
		t.Errorf("the session still carries %q after the refresh, want after", session.Actor.Username)
	}
}

// A client that leaves while the account is being read again has nobody left
// to tell why, so the watch ends as quietly as one that leaves during its wait.
func TestWatchStopsQuietlyWhenTheClientLeavesDuringTheCheck(t *testing.T) {
	recorder := &watchRecorder{}
	c := New(Options{Dispatch: recorder.dispatch})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &Session{
		Actor: watchAdmin("desk"), Transport: "web", Lang: "en", Width: 100,
		Reauthorize: func(context.Context) (user.User, error) {
			cancel()
			return user.User{}, context.Canceled
		},
	}

	var out bytes.Buffer
	c.Execute(ctx, session, &out, "watch --interval 1s --count 5 -- user list")

	if got := recorder.calls(); len(got) != 1 {
		t.Errorf("dispatched %d runs, want 1", len(got))
	}
	if strings.Contains(out.String(), "error:") {
		t.Errorf("a client that left was told why the watch stopped:\n%s", out.String())
	}
}

// A demoted account keeps its watch, but its next run is refused by the
// grants it holds now, before any API call is made.
func TestWatchRefusesARunThatTheAccountIsNoLongerGranted(t *testing.T) {
	recorder := &watchRecorder{}
	c := New(Options{Dispatch: recorder.dispatch})
	demoted := user.User{ID: id.New(), Username: "former", Role: user.RoleUser, Status: user.StatusActive}
	session := &Session{
		Actor: watchAdmin("former"), Transport: "web", Lang: "en", Width: 100,
		Reauthorize: func(context.Context) (user.User, error) {
			return demoted, nil
		},
	}

	var out bytes.Buffer
	c.Execute(context.Background(), session, &out, "watch --interval 1s --count 2 -- user list")

	if got := recorder.calls(); len(got) != 1 {
		t.Errorf("dispatched %d runs, want 1: the demoted account's run must be refused before the API", len(got))
	}
	if !strings.Contains(out.String(), "permission denied") {
		t.Errorf("the refused run was not shown as refused:\n%s", out.String())
	}
}
