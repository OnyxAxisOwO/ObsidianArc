package oauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
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
