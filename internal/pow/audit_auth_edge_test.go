package pow

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

// solveAudit brute-forces the nonce for a challenge the way a browser would.
func solveAudit(t *testing.T, ch Challenge) *Solution {
	t.Helper()
	for n := int64(0); n <= ch.MaxNumber; n++ {
		sum := sha256.Sum256([]byte(ch.Salt + strconv.FormatInt(n, 10)))
		if strings.EqualFold(hex.EncodeToString(sum[:]), ch.Challenge) {
			return &Solution{
				Challenge: ch.Challenge,
				Salt:      ch.Salt,
				MaxNumber: ch.MaxNumber,
				Expires:   ch.Expires,
				Signature: ch.Signature,
				Nonce:     n,
			}
		}
	}
	t.Fatal("challenge has no solution in its range")
	return nil
}

// A solution issued at the base tier must not pass once the elevated tier
// applies, and must not be spent by being refused.
func TestVerifyAtRefusesABaseTierSolutionWithoutSpendingIt(t *testing.T) {
	mgr := NewManager([]byte("audit-test-key-0123456789"), nil)

	base, err := mgr.Issue(1000)
	if err != nil {
		t.Fatal(err)
	}
	baseSolution := solveAudit(t, base)
	if err := mgr.VerifyAt(baseSolution, 5000); err != ErrBelowTier {
		t.Fatalf("base-tier solution under elevated tier = %v, want ErrBelowTier", err)
	}
	if err := mgr.Verify(baseSolution); err != nil {
		t.Fatalf("refused solution was spent: %v", err)
	}

	elevated, err := mgr.Issue(5000)
	if err != nil {
		t.Fatal(err)
	}
	elevatedSolution := solveAudit(t, elevated)
	if err := mgr.VerifyAt(elevatedSolution, 5000); err != nil {
		t.Fatalf("elevated-tier solution = %v, want nil", err)
	}
	if err := mgr.VerifyAt(elevatedSolution, 5000); err != ErrReplayed {
		t.Fatalf("second use = %v, want ErrReplayed", err)
	}
}

// The replay table is walked at most once a second; a second accepted solution
// in the same second must not walk it again.
func TestVerifyPrunesTheReplayTableAtMostOncePerSecond(t *testing.T) {
	mgr := NewManager([]byte("audit-test-key-0123456789"), nil)
	at := time.Unix(time.Now().Unix(), 0)

	first, err := mgr.Issue(100)
	if err != nil {
		t.Fatal(err)
	}
	mgr.usedSalts["expired-a"] = at.Unix() - 1
	if err := mgr.verify(solveAudit(t, first), 0, at); err != nil {
		t.Fatal(err)
	}
	if _, kept := mgr.usedSalts["expired-a"]; kept {
		t.Fatal("the first verification did not prune the expired salt")
	}

	second, err := mgr.Issue(100)
	if err != nil {
		t.Fatal(err)
	}
	mgr.usedSalts["expired-b"] = at.Unix() - 1
	if err := mgr.verify(solveAudit(t, second), 0, at.Add(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, kept := mgr.usedSalts["expired-b"]; !kept {
		t.Fatal("a second verification within the same second walked the replay table again")
	}
}
