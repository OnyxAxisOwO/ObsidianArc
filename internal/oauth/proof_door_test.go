package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// withProofOfWork sets the registration captcha to proof of work, with a manager
// wired the way server.go wires it.
func withProofOfWork(t *testing.T, f *fixture) {
	t.Helper()
	f.auth.PoW = pow.NewManager([]byte("a-proof-of-work-test-key"), pow.NewTracker())
	require(t, f, settings.RegistrationCaptchaMode, settings.CaptchaModePoW)
}

// proofQuery is a solved challenge, the way the register page hands it to the
// provider's start request. The range is small so that solving it is quick.
func proofQuery(t *testing.T, f *fixture) string {
	t.Helper()
	challenge, err := f.auth.PoW.Issue(1000)
	if err != nil {
		t.Fatalf("issue a challenge: %v", err)
	}
	for nonce := int64(0); nonce <= challenge.MaxNumber; nonce++ {
		sum := sha256.Sum256([]byte(challenge.Salt + strconv.FormatInt(nonce, 10)))
		if hex.EncodeToString(sum[:]) != challenge.Challenge {
			continue
		}
		encoded, err := json.Marshal(pow.Solution{
			Challenge: challenge.Challenge, Salt: challenge.Salt, MaxNumber: challenge.MaxNumber,
			Expires: challenge.Expires, Signature: challenge.Signature, Nonce: nonce,
		})
		if err != nil {
			t.Fatalf("encode the solution: %v", err)
		}
		return string(encoded)
	}
	t.Fatal("the challenge had no solution in its range")
	return ""
}

// registerStart is the start request the register page makes with its proof.
func registerStart(proof string) string {
	return "/api/auth/oauth/start/github?register=1&pow=" + url.QueryEscape(proof)
}

// A provider sign-up that starts on the register page carries its proof of work
// to the door, and that pass is what lets the account open.
func TestAProviderSignUpThroughTheRegisterPageCarriesItsProofOfWork(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	withProofOfWork(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	_, mux := handlers(t, f)

	back := returnFrom(t, mux, registerStart(proofQuery(t, f)))
	if location := back.Header().Get("Location"); location != "/" {
		t.Fatalf("callback = %q, want the sign-in to finish", location)
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want the new one opened", total)
	}
}

// A start that cannot show a proof that answers the challenge is refused at the
// door: the browser goes back to the register page with no provider round trip,
// and no sign-in is kept.
func TestAProofThatIsMissingBrokenOrWrongStopsTheSignUpAtTheDoor(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	withProofOfWork(t, f)
	f.configure(t, "github")
	_, mux := handlers(t, f)

	var solution pow.Solution
	if err := json.Unmarshal([]byte(proofQuery(t, f)), &solution); err != nil {
		t.Fatalf("decode the solution: %v", err)
	}
	solution.Nonce++
	wrongAnswer, err := json.Marshal(solution)
	if err != nil {
		t.Fatalf("encode the wrong answer: %v", err)
	}

	for name, path := range map[string]string{
		"missing":      "/api/auth/oauth/start/github?register=1",
		"not a proof":  "/api/auth/oauth/start/github?register=1&pow=" + url.QueryEscape("not-a-proof"),
		"wrong answer": registerStart(string(wrongAnswer)),
	} {
		t.Run(name, func(t *testing.T) {
			begun := get(mux, path, nil, nil)
			if location := begun.Header().Get("Location"); location != "/register?oauth_error=challenge_failed" {
				t.Fatalf("start = %q, want the register page told the check failed", location)
			}
			if cookieNamed(begun, stateCookie) != nil {
				t.Error("a sign-in was started for a sign-up that did not pass")
			}
		})
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened", total)
	}
}

// A proof is spent by the start that checks it, so a second start carrying the
// same one is refused and cannot open a second sign-in.
func TestAProofOpensOnlyOneSignIn(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	withProofOfWork(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	_, mux := handlers(t, f)
	proof := proofQuery(t, f)

	if location := returnFrom(t, mux, registerStart(proof)).Header().Get("Location"); location != "/" {
		t.Fatalf("first sign-up = %q, want it to finish", location)
	}
	again := get(mux, registerStart(proof), nil, nil)
	if location := again.Header().Get("Location"); location != "/register?oauth_error=challenge_failed" {
		t.Fatalf("second start = %q, want the spent proof refused", location)
	}
}

// Under both modes the sign-up door needs the Turnstile token and the proof: each
// answer alone is refused, and both together open the account.
func TestUnderBothTheSignUpDoorNeedsTheTokenAndTheProof(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	withProofOfWork(t, f)
	require(t, f, settings.RegistrationCaptchaMode, settings.CaptchaModeBoth)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	h, mux := handlers(t, f)
	h.SignupChallenge = (&signupSwitch{on: true}).gate()
	proof := proofQuery(t, f)

	for name, path := range map[string]string{
		"proof without the token": registerStart(proof),
		"token without the proof": "/api/auth/oauth/start/github?register=1&turnstile=good",
	} {
		begun := get(mux, path, nil, nil)
		if location := begun.Header().Get("Location"); location != "/register?oauth_error=challenge_failed" {
			t.Fatalf("%s: start = %q, want it refused", name, location)
		}
	}
	if location := returnFrom(t, mux, registerStart(proof)+"&turnstile=good").Header().Get("Location"); location != "/" {
		t.Fatalf("both answered = %q, want the sign-in to finish", location)
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want the new one opened once both were answered", total)
	}
}

// A sign-up started from the login page is held to the proof at its callback,
// just as it is held to the Turnstile check, and is refused before the details
// form is offered.
func TestAProviderSignUpFromTheLoginPageIsRefusedWhileAProofIsRequired(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	withProofOfWork(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	_, mux := handlers(t, f)

	back := returnFrom(t, mux, "/api/auth/oauth/start/github")
	if location := back.Header().Get("Location"); location != "/login?oauth_error=signup_challenge_required" {
		t.Fatalf("callback = %q, want the sign-up held to the challenge", location)
	}
	if total := accountCount(t, f); total != 1 {
		t.Errorf("accounts = %d, want none opened past the challenge", total)
	}
	if _, err := f.store.Account(t.Context(), nil, "github", "4218"); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("identity = %v, want it left unconnected", err)
	}
}

// Signing in to an account that already holds the provider asks nothing of it:
// the login door has no proof, and the account opens nothing.
func TestASignInToAnExistingAccountNeedsNoProofOfWork(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	withProofOfWork(t, f)
	if _, err := f.service.SignIn(t.Context(), identity("4218", "octocat", ""),
		cleared, "203.0.113.5", "a browser"); err != nil {
		t.Fatalf("open the account: %v", err)
	}
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	_, mux := handlers(t, f)

	back := returnFrom(t, mux, "/api/auth/oauth/start/github")
	if location := back.Header().Get("Location"); location != "/" {
		t.Fatalf("callback = %q, want the existing account signed in", location)
	}
	if total := accountCount(t, f); total != 2 {
		t.Errorf("accounts = %d, want no new one", total)
	}
}

// The details form a sign-up can stop at opens the account on the pass its start
// recorded, so the form asks for no second proof.
func TestAPendingSignUpFromTheRegisterPageIsOpenedWithoutASecondProof(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	require(t, f, badgeRule, auth.FieldRequired)
	withProofOfWork(t, f)
	f.configure(t, "github")
	stubGitHubAs(t, "4218", "octocat")
	_, mux := handlers(t, f)

	back := returnFrom(t, mux, registerStart(proofQuery(t, f)))
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

// The sign-up door is challenged in exactly the modes Register challenges a
// sign-up: proof of work in pow and both, and the Turnstile gate when its switch
// is on.
func TestTheSignUpDoorIsChallengedInEveryModeThatAsksForOne(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		turnstile  bool
		challenged bool
	}{
		{"off", settings.CaptchaModeOff, false, false},
		{"turnstile with its switch off", settings.CaptchaModeTurnstile, false, false},
		{"turnstile with its switch on", settings.CaptchaModeTurnstile, true, true},
		{"pow", settings.CaptchaModePoW, false, true},
		{"both", settings.CaptchaModeBoth, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			populate(t, f)
			require(t, f, settings.RegistrationCaptchaMode, tc.mode)
			h, _ := handlers(t, f)
			h.SignupChallenge = (&signupSwitch{on: tc.turnstile}).gate()

			if open := h.admission(false).SignUpCleared; open == tc.challenged {
				t.Errorf("admission cleared = %v, want the challenge held to be %v", open, tc.challenged)
			}
		})
	}
}
