package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func (l *imageLab) askBody() string {
	return `{"model_id":"` + l.painter.ID + `","prompt":"a sunset"`
}

func (l *imageLab) paintedOnce() bool { return len(l.fixture.upstream.calls) > 0 }

func errorCode(t *testing.T, body string) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not an error body: %q", body)
	}
	return out.Error.Code
}

func solve(t *testing.T, mgr *pow.Manager) *pow.Solution {
	t.Helper()
	c, err := mgr.Issue(1000)
	if err != nil {
		t.Fatal(err)
	}
	for n := int64(0); n <= c.MaxNumber; n++ {
		sum := sha256.Sum256([]byte(c.Salt + strconv.FormatInt(n, 10)))
		if hex.EncodeToString(sum[:]) == c.Challenge {
			return &pow.Solution{
				Challenge: c.Challenge, Salt: c.Salt, MaxNumber: c.MaxNumber,
				Expires: c.Expires, Signature: c.Signature, Nonce: n,
			}
		}
	}
	t.Fatal("challenge has no solution")
	return nil
}

// A challenge that is switched off asks nothing: the lab worked before the
// switches existed and must keep working for the instance that never sets one.
func TestImageChallengesAreOffUntilSwitchedOn(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)
	if code := lab.generate(t, lab.askBody()+`}`).Code; code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

// Turnstile: no token and a refused token are both refused before the
// provider is called, and a good one goes through.
func TestImageLabTurnstileStandsBeforeTheProvider(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.handlers.ImageChallenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify: func(_ context.Context, token, _ string) error {
			if token != "good" {
				return turnstile.ErrFailed
			}
			return nil
		},
	}
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	for _, body := range []string{lab.askBody() + `}`, lab.askBody() + `,"turnstile":"bad"}`} {
		recorder := lab.generate(t, body)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder.Body.String()) != "challenge_failed" {
			t.Fatalf("got %d %s, want 403 challenge_failed", recorder.Code, recorder.Body.String())
		}
	}
	if lab.paintedOnce() {
		t.Fatal("the provider was called for a refused challenge")
	}
	if code := lab.generate(t, lab.askBody()+`,"turnstile":"good"}`).Code; code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

func TestImageLabTurnstileOutageIsNotTheVisitorsFault(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.handlers.ImageChallenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify:  func(context.Context, string, string) error { return turnstile.ErrUnavailable },
	}
	recorder := lab.generate(t, lab.askBody()+`,"turnstile":"x"}`)
	if recorder.Code != http.StatusServiceUnavailable || errorCode(t, recorder.Body.String()) != "challenge_unavailable" {
		t.Fatalf("got %d %s, want 503 challenge_unavailable", recorder.Code, recorder.Body.String())
	}
}

// Proof of work: required, checked, and good for one picture only.
func TestImageLabProofOfWorkIsRequiredAndSpentOnce(t *testing.T) {
	lab := newImageLab(t, 1)
	mgr := pow.NewManager([]byte("a-test-key-for-the-lab"), nil)
	lab.handlers.ImagePoW = mgr
	lab.handlers.ImagePoWEnabled = func() bool { return true }
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	recorder := lab.generate(t, lab.askBody()+`}`)
	if recorder.Code != http.StatusBadRequest || errorCode(t, recorder.Body.String()) != "pow_required" {
		t.Fatalf("got %d %s, want 400 pow_required", recorder.Code, recorder.Body.String())
	}

	solution := solve(t, mgr)
	payload, _ := json.Marshal(solution)
	body := lab.askBody() + `,"pow":` + string(payload) + `}`
	if code := lab.generate(t, body).Code; code != http.StatusOK {
		t.Fatalf("a solved challenge got %d, want 200", code)
	}
	recorder = lab.generate(t, body)
	if recorder.Code != http.StatusBadRequest || errorCode(t, recorder.Body.String()) != "pow_replayed" {
		t.Fatalf("a replay got %d %s, want 400 pow_replayed", recorder.Code, recorder.Body.String())
	}

	forged := *solution
	forged.Nonce++
	forged.Salt = "forged"
	payload, _ = json.Marshal(forged)
	recorder = lab.generate(t, lab.askBody()+`,"pow":`+string(payload)+`}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("a forged solution got %d, want 400", recorder.Code)
	}
}

// Plugin guards get the tokens under their own names, and what a guard says
// is what the visitor is told.
func TestImageLabGuardsGetTheirTokensAndSpeakForThemselves(t *testing.T) {
	lab := newImageLab(t, 1)
	var seen map[string]string
	lab.handlers.ImageGuards = func(_ context.Context, tokens map[string]string, _, username string) error {
		seen = tokens
		if tokens["riskcontrol"] != "ok" {
			return &httpx.Error{Status: http.StatusForbidden, Code: "risk_refused", Message: "No."}
		}
		if username == "" {
			return errors.New("the guard was not told who is asking")
		}
		return nil
	}
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	recorder := lab.generate(t, lab.askBody()+`,"guards":{"riskcontrol":"no"}}`)
	if recorder.Code != http.StatusForbidden || errorCode(t, recorder.Body.String()) != "risk_refused" {
		t.Fatalf("got %d %s, want the guard's own 403", recorder.Code, recorder.Body.String())
	}
	if lab.paintedOnce() {
		t.Fatal("the provider was called for a refused guard")
	}
	if code := lab.generate(t, lab.askBody()+`,"guards":{"riskcontrol":"ok"}}`).Code; code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (guards seen: %v)", code, seen)
	}
}

// An administrator testing the lab is not the traffic the challenges are for,
// the way the chat's speed challenge treats one.
func TestImageLabChallengesStandAsideForAdministrators(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.account.Role = user.RoleAdmin
	lab.handlers.ImageGuards = func(context.Context, map[string]string, string, string) error {
		return errors.New("an administrator was asked")
	}
	lab.handlers.ImageChallenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify:  func(context.Context, string, string) error { return turnstile.ErrFailed },
	}
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)
	if recorder := lab.generate(t, lab.askBody()+`}`); recorder.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200", recorder.Code, recorder.Body.String())
	}
}
