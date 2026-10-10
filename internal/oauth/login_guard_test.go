package oauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// loginGuard stands a plugin's login guard in front of sign-in, and records each
// question it is asked. refuse turns every sign-in away.
func loginGuard(f *fixture, refuse bool) *[]auth.GuardRequest {
	asked := &[]auth.GuardRequest{}
	f.auth.AddGuard(auth.GuardLogin, auth.Guard{
		Name:   "risk",
		Plugin: "risk",
		Event:  "login_refused",
		Check: func(_ context.Context, request auth.GuardRequest) (auth.Verdict, error) {
			*asked = append(*asked, request)
			if refuse {
				return auth.Verdict{}, &auth.GuardRefusal{
					Err:    errors.New("the risk service refused this sign-in"),
					Reason: "score too high",
				}
			}
			return auth.Verdict{}, nil
		},
	})
	return asked
}

// A provider sign-in to an account that already exists is a sign-in like any
// other, so the login guards stand in front of it as they stand in front of the
// sign-in form. The refusal reaches the sign-in page as a code, and nothing
// opens a session.
func TestAProviderSignInIsRefusedByALoginGuard(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	if _, err := f.service.SignIn(context.Background(), identity("4218", "octocat", ""),
		cleared, "203.0.113.5", "a browser"); err != nil {
		t.Fatalf("open the account: %v", err)
	}
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	asked := loginGuard(f, true)
	_, mux := handlers(t, f)

	back := returnFrom(t, mux, "/api/auth/oauth/start/github")
	if location := back.Header().Get("Location"); !strings.Contains(location, "oauth_error=login_refused") {
		t.Fatalf("callback = %q, want the sign-in refused by the guard", location)
	}
	if cookieNamed(back, "obsidian_session") != nil {
		t.Error("a refused sign-in still set a session cookie")
	}
	if len(*asked) != 1 || (*asked)[0].Action != auth.GuardLogin || (*asked)[0].Username != "octocat" {
		t.Fatalf("guard asked = %+v, want the one sign-in by octocat at the login door", *asked)
	}
}

// The same account, with a login guard that lets the sign-in through, signs in.
func TestAProviderSignInIsOpenWhenTheLoginGuardsAllowIt(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	if _, err := f.service.SignIn(context.Background(), identity("4218", "octocat", ""),
		cleared, "203.0.113.5", "a browser"); err != nil {
		t.Fatalf("open the account: %v", err)
	}
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	loginGuard(f, false)
	_, mux := handlers(t, f)

	back := returnFrom(t, mux, "/api/auth/oauth/start/github")
	if location := back.Header().Get("Location"); location != "/" {
		t.Fatalf("callback = %q, want the account signed in", location)
	}
	if cookieNamed(back, "obsidian_session") == nil {
		t.Error("an allowed sign-in set no session cookie")
	}
}

// The callback asks the login guards and then stops at the details form. The
// identity is connected from another tab while the form is open, so completing
// the form lands on an existing account, and that sign-in is asked of the guards
// again. A refusal there opens no session.
func TestACompletedSignInIsAskedOfTheLoginGuardsAgain(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, settings.OAuthRequirePassword, "true")
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	asked := 0
	f.auth.AddGuard(auth.GuardLogin, auth.Guard{
		Name:   "risk",
		Plugin: "risk",
		Event:  "login_refused",
		Check: func(_ context.Context, _ auth.GuardRequest) (auth.Verdict, error) {
			asked++
			if asked == 1 {
				return auth.Verdict{}, nil
			}
			return auth.Verdict{}, &auth.GuardRefusal{
				Err:    errors.New("the risk service refused this sign-in"),
				Reason: "later",
			}
		},
	})
	_, mux := handlers(t, f)
	ticket := pendingTicket(t, mux)
	connectFromAnotherTab(t, f)

	done := postJSON(mux, "/api/auth/oauth/signup", map[string]any{}, []*http.Cookie{ticket})
	if done.Code != http.StatusForbidden || !strings.Contains(done.Body.String(), "login_refused") {
		t.Fatalf("complete = %d %s, want the sign-in refused by the guard", done.Code, done.Body.String())
	}
	if cookieNamed(done, "obsidian_session") != nil {
		t.Error("a refused completion set a session cookie")
	}
	if asked != 2 {
		t.Errorf("guard asked %d times, want once at the callback and once at the form", asked)
	}
}

// The same completion with a login guard that lets the sign-in through opens the
// session the form was waiting to open.
func TestACompletedSignInOpensASessionWhenTheLoginGuardsAllowIt(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, settings.OAuthRequirePassword, "true")
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	asked := loginGuard(f, false)
	_, mux := handlers(t, f)
	ticket := pendingTicket(t, mux)
	connectFromAnotherTab(t, f)

	done := postJSON(mux, "/api/auth/oauth/signup", map[string]any{}, []*http.Cookie{ticket})
	if done.Code != http.StatusOK {
		t.Fatalf("complete = %d %s, want the sign-in to open", done.Code, done.Body.String())
	}
	if cookieNamed(done, "obsidian_session") == nil {
		t.Error("an allowed completion set no session cookie")
	}
	if len(*asked) != 2 {
		t.Errorf("guard asked %d times, want once at the callback and once at the form", len(*asked))
	}
}

// connectFromAnotherTab makes the identity stubGitHubAs proves belong to a
// member, the way a second tab would while a details form is open.
func connectFromAnotherTab(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	member, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "member", Email: "member@example.com", Fields: badgeOf("87654321"),
		Password: "another-password",
	})
	if err != nil {
		t.Fatalf("register member: %v", err)
	}
	if err := f.service.Connect(ctx, member.ID, identity("4218", "octocat", "")); err != nil {
		t.Fatalf("connect in another tab: %v", err)
	}
}
