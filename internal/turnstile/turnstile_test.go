package turnstile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A gate with nothing set is off. That is what makes it safe to leave unset:
// every test and every build that never wires one up gets no challenge rather
// than a broken one.
func TestAZeroGateIsOff(t *testing.T) {
	var gate Gate
	if err := gate.Check(context.Background(), "", ""); err != nil {
		t.Errorf("an unset gate refused: %v", err)
	}

	off := Gate{Enabled: func() bool { return false }, Secret: func() string { return "s" }}
	if err := off.Check(context.Background(), "", ""); err != nil {
		t.Errorf("a gate switched off refused: %v", err)
	}
}

// Switched on with no secret is a misconfiguration, not a pass. The one thing
// this must never do is wave a request through that it did not verify.
func TestOnWithoutASecretRefusesRatherThanPassing(t *testing.T) {
	gate := Gate{Enabled: func() bool { return true }, Secret: func() string { return "  " }}
	if err := gate.Check(context.Background(), "a-token", ""); !errors.Is(err, ErrUnavailable) {
		t.Errorf("gave %v, want ErrUnavailable", err)
	}
}

// An empty token never reaches Cloudflare: there is nothing to ask about, and
// the answer is the same either way.
func TestAnEmptyTokenIsRefusedWithoutACall(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	if err := Verify(context.Background(), server.Client(), "secret", "", ""); !errors.Is(err, ErrFailed) {
		t.Errorf("gave %v, want ErrFailed", err)
	}
	if called {
		t.Error("an empty token was sent to be checked")
	}
}

// The operator's own mistakes are told apart from the visitor's. A visitor
// shown "you failed the challenge" when the secret is wrong will try forever.
func TestTheOperatorsMistakeIsNotTheVisitorsFailure(t *testing.T) {
	cases := map[string]error{
		`{"success":false,"error-codes":["invalid-input-secret"]}`:   ErrUnavailable,
		`{"success":false,"error-codes":["missing-input-secret"]}`:   ErrUnavailable,
		`{"success":false,"error-codes":["invalid-input-response"]}`: ErrFailed,
		`{"success":false,"error-codes":["timeout-or-duplicate"]}`:   ErrFailed,
		`{"success":true}`: nil,
	}

	for body, want := range cases {
		reply := body
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The address is passed along where it is known: it is what binds
			// a solved challenge to one caller instead of a thousand.
			if err := r.ParseForm(); err == nil && r.Form.Get("remoteip") != "203.0.113.9" {
				t.Errorf("remoteip = %q", r.Form.Get("remoteip"))
			}
			_, _ = w.Write([]byte(reply))
		}))

		err := verifyAt(context.Background(), server.Client(), server.URL, "secret", "token", "203.0.113.9")
		if want == nil && err != nil {
			t.Errorf("%s: gave %v, want a pass", body, err)
		}
		if want != nil && !errors.Is(err, want) {
			t.Errorf("%s: gave %v, want %v", body, err, want)
		}
		server.Close()
	}
}

// Cloudflare not answering is not a visitor failing.
func TestAnOutageIsNotAFailedChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	err := verifyAt(context.Background(), server.Client(), server.URL, "secret", "token", "")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("gave %v, want ErrUnavailable", err)
	}
	if errors.Is(err, ErrFailed) {
		t.Error("an outage was reported as the visitor's failure")
	}
}
