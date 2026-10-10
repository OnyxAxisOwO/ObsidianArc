package oauth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
)

// signupSwitch is the operator's sign-up Turnstile switch, turned on and off
// the way the security screen turns it. Only the token "good" passes it, so a
// test can tell a sign-up that was checked from one that was not.
type signupSwitch struct{ on bool }

func (s *signupSwitch) gate() turnstile.Gate {
	return turnstile.Gate{
		Enabled: func() bool { return s.on },
		Verify: func(_ context.Context, token, _ string) error {
			if token != "good" {
				return errors.New("the challenge was not passed")
			}
			return nil
		},
	}
}

// signupGuard stands a plugin's sign-up guard in front of sign-up, through the
// auth service the way a plugin installs one, and records each question it is
// asked. refuse turns every sign-up away; restrict lets it through as a
// restriction instead.
func signupGuard(f *fixture, refuse, restrict bool) *[]auth.GuardRequest {
	asked := &[]auth.GuardRequest{}
	f.auth.AddGuard(auth.GuardRegister, auth.Guard{
		Name:   "risk",
		Plugin: "risk",
		Event:  "risk_refused",
		Check: func(_ context.Context, request auth.GuardRequest) (auth.Verdict, error) {
			*asked = append(*asked, request)
			if refuse {
				return auth.Verdict{}, &auth.GuardRefusal{
					Err:    errors.New("the risk service refused this sign-up"),
					Reason: "score too high",
				}
			}
			return auth.Verdict{Restrict: restrict, Reason: "not sure"}, nil
		},
	})
	f.service.SignupGuards = f.auth.CheckSignupGuards
	return asked
}

// stubGitHubAs points GitHub at a stub that proves the identity the test names.
func stubGitHubAs(t *testing.T, subject, login string) {
	t.Helper()
	stub(t, "github", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"a-token"}`))
	}, Identity{Subject: subject, Login: login, Name: "The " + login})
}

// returnFrom takes a provider sign-in from its start to the callback the
// provider sends the browser back to, and hands back what the callback said.
func returnFrom(t *testing.T, mux *http.ServeMux, startPath string) *httptest.ResponseRecorder {
	t.Helper()
	begun := get(mux, startPath, nil, nil)
	if begun.Code != http.StatusFound {
		t.Fatalf("start %s = %d %s", startPath, begun.Code, begun.Body.String())
	}
	target, err := url.Parse(begun.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return get(mux, "/api/auth/oauth/callback/github?code=the-code&state="+target.Query().Get("state"),
		begun.Result().Cookies(), nil)
}

func accountCount(t *testing.T, f *fixture) int {
	t.Helper()
	total, err := f.users.Count(context.Background(), nil)
	if err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	return total
}

func cookieNamed(recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// legacyCookie signs a state the way start did before it carried the sign-up
// flag: the same key and the same fields, and nothing about the sign-up door.
func legacyCookie(h *Handlers, nonce string) *http.Cookie {
	body := fmt.Sprintf(`{"p":"github","n":%q,"e":%d}`, nonce, time.Now().Add(stateTTL).UnixMilli())
	encoded := base64.RawURLEncoding.EncodeToString([]byte(body))
	return &http.Cookie{Name: stateCookie, Value: encoded + "." + h.stamp.tag(encoded)}
}

// A provider sign-up that never went through the sign-up door is refused at the
// callback, while the challenge is on. It is refused before the details form is
// offered, so nobody is asked for their details and then turned away.
func TestAProviderSignUpThatSkippedTheSignUpChallengeIsRefused(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, badgeRule, auth.FieldRequired)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	sw := &signupSwitch{on: true}
	h.SignupChallenge = sw.gate()

	// The login page's provider button sends no register flag.
	back := returnFrom(t, mux, "/api/auth/oauth/start/github")
	if location := back.Header().Get("Location"); location != "/login?oauth_error=signup_challenge_required" {
		t.Fatalf("callback = %q, want the challenge asked for before any form", location)
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened past the challenge", total)
	}
	if _, err := f.store.Account(context.Background(), nil, "github", "4218"); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("identity = %v, want it left unconnected", err)
	}
	if cookieNamed(back, pendingCookie) != nil {
		t.Error("a sign-up was parked for a challenge it never passed")
	}
}

// The register page sends register=1 and passes the challenge at its own door,
// so its sign-ups are unaffected.
func TestAProviderSignUpThroughTheRegisterPageStillOpensAnAccount(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()

	back := returnFrom(t, mux, "/api/auth/oauth/start/github?register=1&turnstile=good")
	if location := back.Header().Get("Location"); location != "/" {
		t.Fatalf("callback = %q, want the sign-in to finish", location)
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want the new one opened", total)
	}
}

// With the challenge switched off, a provider sign-up opens an account as it
// always did, whichever door it came through.
func TestWithTheSignUpChallengeOffAProviderSignUpOpensAnAccount(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: false}).gate()

	back := returnFrom(t, mux, "/api/auth/oauth/start/github")
	if location := back.Header().Get("Location"); location != "/" {
		t.Fatalf("callback = %q, want the sign-in to finish", location)
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want the new one opened", total)
	}
}

// Signing in to an account that already holds the provider is not opening one.
// It is not held to the challenge, and no guard is asked about it, even one
// that would refuse a new sign-up.
func TestASignInToAnExistingAccountIsNotChallengedOrGuarded(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	ctx := context.Background()
	first, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), cleared, "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("open the account: %v", err)
	}

	asked := signupGuard(f, true, false)
	again, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), Admission{}, "203.0.113.5", "a browser")
	if err != nil || again.ID != first.ID {
		t.Fatalf("sign in again = %v, %v; want the same account with no challenge", again.ID, err)
	}
	if len(*asked) != 0 {
		t.Errorf("guards were asked about an existing account: %+v", *asked)
	}

	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()
	back := returnFrom(t, mux, "/api/auth/oauth/start/github")
	if location := back.Header().Get("Location"); location != "/" {
		t.Fatalf("callback = %q, want the existing account signed in", location)
	}
	if len(*asked) != 0 {
		t.Errorf("guards were asked on the provider round trip: %+v", *asked)
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want no new one", total)
	}
}

// The first account is the one that makes this instance administered, so
// neither the challenge nor the guards stand in front of it, the way Register
// exempts it.
func TestTheFirstAccountIsNeitherChallengedNorGuarded(t *testing.T) {
	f := newFixture(t)
	asked := signupGuard(f, true, false)

	if _, err := f.service.SignIn(context.Background(), identity("4218", "octocat", ""),
		Admission{}, "203.0.113.5", "a browser"); err != nil {
		t.Fatalf("the first account was refused: %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("guards were asked about the first account: %+v", *asked)
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want the first one opened", total)
	}
}

// A plugin's sign-up guard that refuses stops the account from being written,
// and it is asked with the name and address the sign-up would have carried.
func TestASignupGuardRefusalStopsProvisioning(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	asked := signupGuard(f, true, false)

	_, err := f.service.SignIn(context.Background(), identity("4218", "octocat", ""),
		cleared, "203.0.113.5", "a browser")
	if !errors.Is(err, ErrSignupRefused) {
		t.Fatalf("sign in = %v, want the sign-up refused", err)
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened by a refused sign-up", total)
	}
	if _, err := f.store.Account(context.Background(), nil, "github", "4218"); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("identity = %v, want it left unconnected", err)
	}
	if len(*asked) != 1 || (*asked)[0].Username != "octocat" || (*asked)[0].IP != "203.0.113.5" {
		t.Fatalf("guard asked = %+v, want the one sign-up with its name and address", *asked)
	}
}

// A guard that cannot answer refuses, the same as one that says no.
func TestAGuardThatCannotAnswerRefuses(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.service.SignupGuards = func(context.Context, string, string) (auth.Verdict, error) {
		return auth.Verdict{}, errors.New("the risk service is down")
	}

	if _, err := f.service.SignIn(context.Background(), identity("4218", "octocat", ""),
		cleared, "203.0.113.5", "a browser"); !errors.Is(err, ErrSignupRefused) {
		t.Fatalf("sign in = %v, want the sign-up refused", err)
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened", total)
	}
}

// A guard that lets a sign-up through without being sure of it opens the
// account with its API access closed, the way a sign-up review restriction
// does when Register opens an account.
func TestASignupGuardThatRestrictsOpensTheAccountRestricted(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	signupGuard(f, false, true)
	ctx := context.Background()

	account, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), cleared, "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	stored, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatalf("read the account back: %v", err)
	}
	if !stored.APIRestricted || stored.APIRestrictionSource != "signup_review" {
		t.Errorf("account restricted = %v from %q, want it restricted as a review restricts",
			stored.APIRestricted, stored.APIRestrictionSource)
	}
}

// The form is the other way an account opens, so the guards answer there too,
// once the person has answered the question the form asks.
func TestASignupGuardAnswersTheDetailsFormToo(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, badgeRule, auth.FieldRequired)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	_, mux := handlers(t, f)
	asked := signupGuard(f, true, false)

	ticket := pendingTicket(t, mux)
	if len(*asked) != 0 {
		t.Fatalf("the guards were asked before the form was answered: %+v", *asked)
	}
	done := postJSON(mux, "/api/auth/oauth/signup",
		map[string]any{"fields": badgeOf("87654321")}, []*http.Cookie{ticket})
	if done.Code != http.StatusForbidden || !strings.Contains(done.Body.String(), "signup_refused") {
		t.Fatalf("complete = %d %s, want the sign-up refused", done.Code, done.Body.String())
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened by a refused form", total)
	}
	if len(*asked) != 1 {
		t.Errorf("guard asked %d times, want once", len(*asked))
	}
}

// The register page's flow can end at the details form. The form opens the
// account, and it is opened with the challenge already passed at the door, so
// the person is not challenged a second time.
func TestAPendingSignUpThroughTheRegisterPageIsOpenedFromTheForm(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, badgeRule, auth.FieldRequired)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()

	back := returnFrom(t, mux, "/api/auth/oauth/start/github?register=1&turnstile=good")
	if location := back.Header().Get("Location"); location != "/oauth/complete" {
		t.Fatalf("callback = %q, want the form that asks", location)
	}
	ticket := cookieNamed(back, pendingCookie)
	if ticket == nil {
		t.Fatal("no sign-up was parked for the form")
	}
	done := postJSON(mux, "/api/auth/oauth/signup",
		map[string]any{"fields": badgeOf("87654321")}, []*http.Cookie{ticket})
	if done.Code != http.StatusOK || !strings.Contains(done.Body.String(), `"redirect":"/"`) {
		t.Fatalf("complete = %d %s, want the account opened", done.Code, done.Body.String())
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want the new one opened from the form", total)
	}
}

// A form parked while the challenge was off is answered under the challenge
// that is on when the answer comes back: the door it came through decides, not
// the switch as it was when the question was asked.
func TestAFormParkedBeforeTheChallengeWasTurnedOnIsHeldToIt(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, badgeRule, auth.FieldRequired)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	sw := &signupSwitch{on: false}
	h.SignupChallenge = sw.gate()

	ticket := pendingTicket(t, mux)
	sw.on = true

	done := postJSON(mux, "/api/auth/oauth/signup",
		map[string]any{"fields": badgeOf("87654321")}, []*http.Cookie{ticket})
	if done.Code != http.StatusForbidden || !strings.Contains(done.Body.String(), "signup_challenge_required") {
		t.Fatalf("complete = %d %s, want it held to the challenge", done.Code, done.Body.String())
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened past the challenge", total)
	}
}

// A state signed before it carried the sign-up flag did not pass the challenge,
// so while the challenge is on it cannot open an account.
func TestAStateSignedBeforeTheFlagExistedIsHeldToTheChallenge(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()

	back := get(mux, "/api/auth/oauth/callback/github?code=the-code&state=a-nonce",
		[]*http.Cookie{legacyCookie(h, "a-nonce")}, nil)
	if location := back.Header().Get("Location"); location != "/login?oauth_error=signup_challenge_required" {
		t.Fatalf("callback = %q, want it held to the challenge", location)
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened", total)
	}
}

// Both refusals reach the browser and the form under their own codes, so the
// screens can say what happened in the reader's language.
func TestTheSignUpRefusalsAreCodedForTheCallbackAndTheForm(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		callback string
		code     string
	}{
		{"challenge", ErrSignupChallengeRequired, "signup_challenge_required", "signup_challenge_required"},
		{"guard", ErrSignupRefused, "signup_refused", "signup_refused"},
		{"guard wrapped", fmt.Errorf("guard: %w", ErrSignupRefused), "signup_refused", "signup_refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := signInFailure(tc.err); got != tc.callback {
				t.Errorf("callback failure = %q, want %q", got, tc.callback)
			}
			var response *httpx.Error
			if !errors.As(completionError(tc.err), &response) {
				t.Fatalf("completion mapping = %v, want an httpx error", completionError(tc.err))
			}
			if response.Status != http.StatusForbidden || response.Code != tc.code {
				t.Errorf("completion mapping = %d/%s, want %d/%s",
					response.Status, response.Code, http.StatusForbidden, tc.code)
			}
		})
	}
}
