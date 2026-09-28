package riskcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The stub stands in for the service's /api/siteverify: it records what this
// server sent and answers with whatever verdict the test scripted, so the
// assertions can cover both halves of the wire.
type stub struct {
	requests atomic.Int32
	lastBody verifyRequest
	// What to answer with, by the token that arrived. A token missing from
	// the map is answered as the service answers a stale one.
	verdicts map[string]verifyResponse
	// Where the test wants a transport-level failure instead.
	status int
}

func (s *stub) handler(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	var body verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.lastBody = body

	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	verdict, ok := s.verdicts[body.Token]
	if !ok {
		verdict = verifyResponse{Success: false, Codes: []string{"invalid-input-response"}}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(verdict)
}

func startStub(t *testing.T, verdicts map[string]verifyResponse) (*stub, *httptest.Server) {
	t.Helper()
	s := &stub{verdicts: verdicts}
	server := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(server.Close)
	return s, server
}

func TestVerifyAsksForTheRightVerdict(t *testing.T) {
	s, server := startStub(t, map[string]verifyResponse{
		"good": {Success: true, Score: 0.9, Action: "register", Decision: DecisionAllow},
	})

	result, err := Verify(context.Background(), server.Client(), server.URL+"/api/siteverify", "shop-a", "rk_secret", "good", ActionRegister, "203.0.113.7")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Decision != DecisionAllow || result.Score != 0.9 {
		t.Fatalf("verdict = %+v, want allow/0.9", result)
	}

	// Everything the service needs to bind the token to this instance and
	// this visitor must be on the wire, and the secret must be in the body —
	// it is the one thing that makes the reported address believable.
	sent := s.lastBody
	if sent.Token != "good" || sent.Action != ActionRegister || sent.Site != "shop-a" || sent.Secret != "rk_secret" || sent.RemoteIP != "203.0.113.7" {
		t.Fatalf("request body = %+v", sent)
	}
}

func TestVerifyMapsTheDecisionBands(t *testing.T) {
	cases := map[string]struct {
		verdict verifyResponse
		want    string
		wantErr error
	}{
		"challenge passes through": {
			verdict: verifyResponse{Success: true, Score: 0.5, Decision: DecisionChallenge},
			want:    DecisionChallenge,
		},
		"block passes through": {
			verdict: verifyResponse{Success: true, Score: 0.1, Decision: DecisionBlock},
			want:    DecisionBlock,
		},
		"an unknown band reads as allow": {
			verdict: verifyResponse{Success: true, Score: 0.8, Decision: "probably"},
			want:    DecisionAllow,
		},
		"a refused token is the reader's failure": {
			verdict: verifyResponse{Success: false, Codes: []string{"invalid-input-response"}},
			wantErr: ErrFailed,
		},
		"a spent token is the reader's failure": {
			verdict: verifyResponse{Success: false, Codes: []string{"timeout-or-duplicate"}},
			wantErr: ErrFailed,
		},
		"the site not existing is the operator's": {
			verdict: verifyResponse{Success: false, Codes: []string{"unknown-site"}},
			wantErr: ErrUnavailable,
		},
		"the wrong secret is the operator's": {
			verdict: verifyResponse{Success: false, Codes: []string{"invalid-input-secret"}},
			wantErr: ErrUnavailable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, server := startStub(t, map[string]verifyResponse{"t": tc.verdict})
			result, err := Verify(context.Background(), server.Client(), server.URL+"/api/siteverify", "site", "secret", "t", ActionRegister, "")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if result.Decision != tc.want {
				t.Fatalf("decision = %q, want %q", result.Decision, tc.want)
			}
			if s.lastBody.RemoteIP != "" {
				t.Fatalf("remoteip sent for an unknown address: %+v", s.lastBody)
			}
		})
	}
}

func TestVerifyAnswersByTheServiceFaults(t *testing.T) {
	s, server := startStub(t, nil)
	s.status = http.StatusInternalServerError
	if _, err := Verify(context.Background(), server.Client(), server.URL+"/api/siteverify", "site", "secret", "t", ActionLogin, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable for a 500", err)
	}
}

func TestVerifyRefusesAnUnusableGate(t *testing.T) {
	ctx := context.Background()
	// A token that cannot exist — there was no check to pass.
	if _, err := Verify(ctx, nil, "https://risk.example.com/api/siteverify", "site", "secret", "", ActionLogin, ""); !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed for an empty token", err)
	}
	// Half a configuration is not a check this server can stand behind.
	if _, err := Verify(ctx, nil, "https://risk.example.com/api/siteverify", "site", "", "t", ActionLogin, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable for a missing secret", err)
	}
	if _, err := Verify(ctx, nil, "", "site", "secret", "t", ActionLogin, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable for a missing endpoint", err)
	}
}

func TestEndpointResolvesBothDeploymentShapes(t *testing.T) {
	cases := map[string]struct {
		base, publicOrigin, want string
	}{
		"an absolute base is used as it stands": {
			base: "https://risk.example.com", want: "https://risk.example.com/api/siteverify",
		},
		"a trailing slash does not double": {
			base: "https://risk.example.com/", want: "https://risk.example.com/api/siteverify",
		},
		"a path base resolves against the public origin": {
			base: "/rc", publicOrigin: "https://arc.example.com", want: "https://arc.example.com/rc/api/siteverify",
		},
		"a path base with no public origin reads as unconfigured": {
			base: "/rc", want: "",
		},
		"an empty base reads as unconfigured": {
			want: "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Endpoint(tc.base, tc.publicOrigin); got != tc.want {
				t.Fatalf("Endpoint(%q, %q) = %q, want %q", tc.base, tc.publicOrigin, got, tc.want)
			}
		})
	}
}

func TestZeroGateIsOff(t *testing.T) {
	var gate Gate
	if result, err := gate.Check(context.Background(), "t", ActionRegister, ""); err != nil || result != (Result{}) {
		t.Fatalf("zero gate answered %+v, %v; want off", result, err)
	}
}

func TestGateCanBeReplacedForTests(t *testing.T) {
	called := false
	gate := Gate{
		Enabled: func() bool { return true },
		Verify: func(context.Context, string, string, string) (Result, error) {
			called = true
			return Result{Score: 0.4, Decision: DecisionChallenge}, nil
		},
	}
	result, err := gate.Check(context.Background(), "t", ActionLogin, "")
	if err != nil || !called || result.Decision != DecisionChallenge {
		t.Fatalf("gate seam not used: %+v %v called=%v", result, err, called)
	}
	// And the same gate, switched off, never calls out.
	gate.Enabled = func() bool { return false }
	called = false
	if _, err := gate.Check(context.Background(), "t", ActionLogin, ""); err != nil || called {
		t.Fatalf("disabled gate called the verifier: %v", err)
	}
}
