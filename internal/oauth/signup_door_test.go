package oauth

import (
	"context"
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
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
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
	return &http.Cookie{Name: stateCookie, Value: secret.Sign(h.stamp.key, []byte(body))}
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
	if !stored.APIRestricted || stored.APIRestrictionSource != "guard" {
		t.Errorf("account restricted = %v from %q, want it restricted by the guard",
			stored.APIRestricted, stored.APIRestrictionSource)
	}
	// A guard's restriction lapses on the operator's restrict-hours, as a review's
	// does. Zero until would be indefinite, and only an administrator could lift it.
	now := time.Now().UnixMilli()
	const day = int64(24 * time.Hour / time.Millisecond)
	const slack = int64(time.Hour / time.Millisecond)
	if left := stored.APIRestrictedUntil - now; left < day-slack || left > day+slack {
		t.Errorf("restricted until %d, want about a day from now", stored.APIRestrictedUntil)
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

// A state is a ten-minute ticket, and a copy of its cookie is as good as the
// original until it expires. Once a callback has spent the state, the copy
// presented again opens nothing, even with a provider answer for somebody else.
func TestAReplayedStateCannotOpenASecondAccount(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()

	begun := get(mux, "/api/auth/oauth/start/github?register=1&turnstile=good", nil, nil)
	if begun.Code != http.StatusFound {
		t.Fatalf("start = %d %s", begun.Code, begun.Body.String())
	}
	cookies := begun.Result().Cookies()
	target, err := url.Parse(begun.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	nonce := target.Query().Get("state")

	first := get(mux, "/api/auth/oauth/callback/github?code=the-code&state="+nonce, cookies, nil)
	if location := first.Header().Get("Location"); location != "/" {
		t.Fatalf("first callback = %q, want the sign-in to finish", location)
	}
	if total := accountCount(t, f); total != 2 {
		t.Fatalf("accounts = %d, want the first sign-up opened", total)
	}

	stubGitHubAs(t, "5150", "second")
	replay := get(mux, "/api/auth/oauth/callback/github?code=another-code&state="+nonce, cookies, nil)
	// The replay started on the register page, so the refusal goes back there.
	if location := replay.Header().Get("Location"); location != "/register?oauth_error=state" {
		t.Fatalf("replay = %q, want the state refused as already used", location)
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want no second account opened from a replayed state", total)
	}
	if _, err := f.store.Account(context.Background(), nil, "github", "5150"); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("identity = %v, want the replayed sign-in left unconnected", err)
	}
}

// Spent states are kept only until they expire, because after that the state
// check refuses them on its own. Pruning drops the expired ones and nothing
// still able to answer a callback.
func TestSpentStatesAreDroppedOnceTheyExpire(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	for nonce, expires := range map[string]int64{
		"expired": now - 1000,
		"live":    now + int64(time.Minute/time.Millisecond),
	} {
		spent, err := f.store.SpendState(ctx, nil, nonce, expires)
		if err != nil || !spent {
			t.Fatalf("spend %s = %v, %v; want it spent", nonce, spent, err)
		}
	}

	pruned, err := f.store.PruneStates(ctx, now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want only the expired state", pruned)
	}
	if spent, _ := f.store.SpendState(ctx, nil, "live", now+1000); spent {
		t.Error("a state still within its expiry was spendable again after pruning")
	}
	if spent, _ := f.store.SpendState(ctx, nil, "expired", now-1000); !spent {
		t.Error("an expired state was kept rather than dropped")
	}
}

// A sign-in that fails after starting on the register page goes back to that
// page. The provider's redirect carries no query of its own, so the state is
// what says which page the sign-up began on.
func TestAFailedSignUpReturnsToTheRegisterPage(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.configure(t, "github")
	_, mux := handlers(t, f)

	begun := get(mux, "/api/auth/oauth/start/github?register=1", nil, nil)
	if begun.Code != http.StatusFound {
		t.Fatalf("start = %d %s", begun.Code, begun.Body.String())
	}
	target, err := url.Parse(begun.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	refused := get(mux, "/api/auth/oauth/callback/github?error=access_denied&state="+target.Query().Get("state"),
		begun.Result().Cookies(), nil)
	if location := refused.Header().Get("Location"); location != "/register?oauth_error=denied" {
		t.Fatalf("refused sign-up = %q, want the register page", location)
	}
}

// A form parked for a provider that is then switched off is not completed.
// The callback checks the switch on the way in; the form has to check it too,
// because the ticket outlives the moment the callback looked.
func TestAPendingSignUpIsNotOpenedAfterItsProviderIsSwitchedOff(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, badgeRule, auth.FieldRequired)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()

	back := returnFrom(t, mux, "/api/auth/oauth/start/github?register=1&turnstile=good")
	ticket := cookieNamed(back, pendingCookie)
	if ticket == nil {
		t.Fatal("no sign-up was parked for the form")
	}

	if err := f.settings.Set(context.Background(), settings.OAuthGitHubEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	before := accountCount(t, f)
	done := postJSON(mux, "/api/auth/oauth/signup",
		map[string]any{"fields": badgeOf("87654321")}, []*http.Cookie{ticket})
	if done.Code != http.StatusServiceUnavailable {
		t.Fatalf("complete = %d %s, want the switched-off provider refused", done.Code, done.Body.String())
	}
	if accountCount(t, f) != before {
		t.Error("an account was opened through a provider that is switched off")
	}
}

// The sign-up ticket is a credential like the state, and the form it opens
// spends it. Once a completion has opened the session, the same ticket presented
// again opens nothing.
func TestACompletedSignUpTicketCannotOpenASecondSession(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, badgeRule, auth.FieldRequired)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()

	back := returnFrom(t, mux, "/api/auth/oauth/start/github?register=1&turnstile=good")
	ticket := cookieNamed(back, pendingCookie)
	if ticket == nil {
		t.Fatal("no sign-up was parked for the form")
	}
	first := postJSON(mux, "/api/auth/oauth/signup",
		map[string]any{"fields": badgeOf("87654321")}, []*http.Cookie{ticket})
	if first.Code != http.StatusOK {
		t.Fatalf("first completion = %d %s, want the account opened", first.Code, first.Body.String())
	}
	before := accountCount(t, f)

	replay := postJSON(mux, "/api/auth/oauth/signup",
		map[string]any{"fields": badgeOf("87654321")}, []*http.Cookie{ticket})
	if replay.Code == http.StatusOK {
		t.Fatalf("replayed completion = %d %s, want the spent ticket refused", replay.Code, replay.Body.String())
	}
	if cookieNamed(replay, "obsidian_session") != nil {
		t.Error("a spent sign-up ticket opened a session")
	}
	if accountCount(t, f) != before {
		t.Error("a spent sign-up ticket changed the accounts")
	}
}

// A sign-in that fails keeps the page it was meant to reach. Otherwise an
// authorisation request another site sent the visitor with is dropped at the
// first refusal, and the card has nothing to resume once they try again.
func TestAFailedSignInKeepsTheDestinationItWasFor(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	f.configure(t, "github")
	_, mux := handlers(t, f)

	destination := "/oauth/consent?request=abc"
	begun := get(mux, "/api/auth/oauth/start/github?next="+url.QueryEscape(destination), nil, nil)
	if begun.Code != http.StatusFound {
		t.Fatalf("start = %d %s", begun.Code, begun.Body.String())
	}
	target, err := url.Parse(begun.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	refused := get(mux, "/api/auth/oauth/callback/github?error=access_denied&state="+target.Query().Get("state"),
		begun.Result().Cookies(), nil)

	location, err := url.Parse(refused.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse refusal redirect: %v", err)
	}
	if location.Path != "/login" || location.Query().Get("oauth_error") != "denied" {
		t.Fatalf("refusal = %q, want the login card with the refusal", refused.Header().Get("Location"))
	}
	if got := location.Query().Get("next"); got != destination {
		t.Errorf("next = %q, want %q carried to the card", got, destination)
	}
}
