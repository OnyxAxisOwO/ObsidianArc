package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
)

// A plugin's human check uses the same proof of work as the sign-up door, so
// it must also refuse work done at a tier the address has since left behind.
func TestPluginChallengeRefusesBaseTierWork(t *testing.T) {
	in := newInstance(t)
	mgr := pow.NewManager([]byte("a-test-key-for-the-plugin-check"), nil)
	challenge := pluginChallenge(in.server.settings, mgr,
		func(string) int64 { return 5000 }, nil, func() bool { return false })

	base := solvePoWForTest(t, mgr, 1000)
	payload, _ := json.Marshal(base)
	if err := challenge.Verify(context.Background(), plugin.ChallengeProof{PoW: payload}, "203.0.113.7"); !errors.Is(err, pow.ErrBelowTier) {
		t.Fatalf("base-tier work passed the plugin check after the tier rose: %v", err)
	}

	current := solvePoWForTest(t, mgr, 5000)
	payload, _ = json.Marshal(current)
	if err := challenge.Verify(context.Background(), plugin.ChallengeProof{PoW: payload}, "203.0.113.7"); err != nil {
		t.Fatalf("work at the current tier was refused: %v", err)
	}
}

func solvePoWForTest(t *testing.T, mgr *pow.Manager, maxNumber int64) *pow.Solution {
	t.Helper()
	c, err := mgr.Issue(maxNumber)
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
