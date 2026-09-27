package pow

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPoWLifecycleSuccess(t *testing.T) {
	mgr := NewManager([]byte("super-secret-key-1234"), nil)
	challenge, err := mgr.Issue(1000)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	// Solve challenge by brute-forcing in [0, 1000]
	var foundNonce int64 = -1
	for i := int64(0); i <= challenge.MaxNumber; i++ {
		sum := sha256.Sum256([]byte(challenge.Salt + strconv.FormatInt(i, 10)))
		if strings.EqualFold(hex.EncodeToString(sum[:]), challenge.Challenge) {
			foundNonce = i
			break
		}
	}
	if foundNonce == -1 {
		t.Fatalf("failed to solve valid challenge")
	}

	sol := &Solution{
		Challenge: challenge.Challenge,
		Salt:      challenge.Salt,
		MaxNumber: challenge.MaxNumber,
		Expires:   challenge.Expires,
		Signature: challenge.Signature,
		Nonce:     foundNonce,
	}

	if err := mgr.Verify(sol); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// Replay must fail
	if err := mgr.Verify(sol); err != ErrReplayed {
		t.Fatalf("expected ErrReplayed, got %v", err)
	}
}

func TestPoWInvalidCases(t *testing.T) {
	mgr := NewManager([]byte("super-secret-key-1234"), nil)
	challenge, err := mgr.Issue(100)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	// 1. Missing solution
	if err := mgr.Verify(nil); err != ErrMissingSolution {
		t.Fatalf("expected ErrMissingSolution, got %v", err)
	}

	// 2. Expired
	expiredSol := &Solution{
		Challenge: challenge.Challenge,
		Salt:      challenge.Salt,
		MaxNumber: challenge.MaxNumber,
		Expires:   time.Now().Unix() - 10,
		Signature: challenge.Signature,
		Nonce:     0,
	}
	if err := mgr.Verify(expiredSol); err != ErrExpired {
		t.Fatalf("expected ErrExpired, got %v", err)
	}

	// 3. Max exceeded
	exceededSol := &Solution{
		Challenge: challenge.Challenge,
		Salt:      challenge.Salt,
		MaxNumber: challenge.MaxNumber,
		Expires:   challenge.Expires,
		Signature: challenge.Signature,
		Nonce:     challenge.MaxNumber + 1,
	}
	if err := mgr.Verify(exceededSol); err != ErrMaxExceeded {
		t.Fatalf("expected ErrMaxExceeded, got %v", err)
	}

	// 4. Invalid signature
	tamperedSigSol := &Solution{
		Challenge: challenge.Challenge,
		Salt:      challenge.Salt,
		MaxNumber: challenge.MaxNumber,
		Expires:   challenge.Expires,
		Signature: "deadbeef",
		Nonce:     0,
	}
	if err := mgr.Verify(tamperedSigSol); err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}

	// 5. Tampered maxNumber (making signature invalid)
	tamperedMaxSol := &Solution{
		Challenge: challenge.Challenge,
		Salt:      challenge.Salt,
		MaxNumber: 99999,
		Expires:   challenge.Expires,
		Signature: challenge.Signature,
		Nonce:     0,
	}
	if err := mgr.Verify(tamperedMaxSol); err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature on tampered maxNumber, got %v", err)
	}
}

func TestTrackerDynamicDifficultyAndRateLimit(t *testing.T) {
	tracker := NewTracker()
	now := time.Now()
	ip := "198.51.100.9"

	// Initial difficulty should be base
	diff := tracker.DetermineMaxNumber(ip, 50000, 500000, 5, now)
	if diff != 50000 {
		t.Fatalf("expected base difficulty 50000, got %d", diff)
	}

	// Record 4 attempts
	for i := 0; i < 4; i++ {
		tracker.RecordAttempt(ip, now.Add(time.Duration(i)*time.Second))
	}
	if d := tracker.DetermineMaxNumber(ip, 50000, 500000, 5, now); d != 50000 {
		t.Fatalf("expected still base difficulty 50000, got %d", d)
	}

	// 5th attempt triggers elevated difficulty
	tracker.RecordAttempt(ip, now.Add(5*time.Second))
	if d := tracker.DetermineMaxNumber(ip, 50000, 500000, 5, now); d != 500000 {
		t.Fatalf("expected elevated difficulty 500000, got %d", d)
	}

	// Site-wide trigger test on another IP
	otherIP := "198.51.100.10"
	if d := tracker.DetermineMaxNumber(otherIP, 50000, 500000, 5, now); d != 500000 {
		t.Fatalf("expected elevated difficulty for other IP due to site activity, got %d", d)
	}

	// Challenge endpoint rate limit test
	for i := 0; i < 30; i++ {
		if err := tracker.CheckChallengeRate(ip, now); err != nil {
			t.Fatalf("unexpected rate limit at attempt %d: %v", i, err)
		}
	}
	if err := tracker.CheckChallengeRate(ip, now); err != ErrRateLimited {
		t.Fatalf("expected ErrRateLimited after 30 requests, got %v", err)
	}
}
