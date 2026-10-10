package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
)

// solveProofForTest solves a challenge the way the register page's worker does.
func solveProofForTest(t *testing.T, challenge pow.Challenge) *pow.Solution {
	t.Helper()
	for nonce := int64(0); nonce <= challenge.MaxNumber; nonce++ {
		sum := sha256.Sum256([]byte(challenge.Salt + strconv.FormatInt(nonce, 10)))
		if hex.EncodeToString(sum[:]) == challenge.Challenge {
			return &pow.Solution{
				Challenge: challenge.Challenge, Salt: challenge.Salt, MaxNumber: challenge.MaxNumber,
				Expires: challenge.Expires, Signature: challenge.Signature, Nonce: nonce,
			}
		}
	}
	t.Fatal("no nonce solves the challenge")
	return nil
}

// A provider sign-up's proof is checked the way a form's is: a missing or spent
// solution is refused, each refusal is logged under the same event, and a
// server with no proof-of-work manager refuses rather than passing.
func TestCheckSignupProofRefusesWhatRegisterRefusesAndLogsIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var logged []string
	f.auth.OnChallengeFailure = func(_ context.Context, event, ip, _, reason string) {
		logged = append(logged, event+"/"+ip+"/"+reason)
	}

	f.auth.PoW = nil
	if err := f.auth.CheckSignupProof(ctx, "203.0.113.5", nil); err == nil || errors.Is(err, pow.ErrMissingSolution) {
		t.Fatalf("without a manager = %v, want a refusal of its own", err)
	}

	f.auth.PoW = pow.NewManager([]byte("a-proof-check-test-key"), pow.NewTracker())
	if err := f.auth.CheckSignupProof(ctx, "203.0.113.5", nil); !errors.Is(err, pow.ErrMissingSolution) {
		t.Fatalf("missing solution = %v, want ErrMissingSolution", err)
	}

	challenge, err := f.auth.PoW.Issue(100)
	if err != nil {
		t.Fatalf("issue a challenge: %v", err)
	}
	solution := solveProofForTest(t, challenge)
	if err := f.auth.CheckSignupProof(ctx, "203.0.113.5", solution); err != nil {
		t.Fatalf("a solved challenge = %v, want it accepted", err)
	}
	if err := f.auth.CheckSignupProof(ctx, "203.0.113.5", solution); !errors.Is(err, pow.ErrReplayed) {
		t.Fatalf("spent solution = %v, want ErrReplayed", err)
	}

	want := []string{
		"pow_challenge/203.0.113.5/缺少 PoW 解答",
		"pow_challenge/203.0.113.5/PoW 挑战已被使用",
	}
	if len(logged) != len(want) || logged[0] != want[0] || logged[1] != want[1] {
		t.Errorf("logged = %q, want %q", logged, want)
	}
}
