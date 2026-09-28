// Package pow implements a bounded proof-of-work challenge (ALTCHA-style)
// to defeat automated registration scripts without external dependencies.
//
// The server issues a challenge derived from a random salt and a bounded secret
// number. The client increments from 0 up to maxNumber until finding the matching
// nonce. The challenge is authenticated via HMAC-SHA256 and salt replay is
// guarded in memory.
package pow

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrMissingSolution  = errors.New("pow: solution is required")
	ErrExpired          = errors.New("pow: challenge has expired")
	ErrInvalidSignature = errors.New("pow: invalid signature")
	ErrMaxExceeded      = errors.New("pow: nonce exceeds max number")
	ErrInvalidNonce     = errors.New("pow: hash does not match challenge")
	ErrReplayed         = errors.New("pow: challenge salt has already been used")
	ErrRateLimited      = errors.New("pow: too many challenge requests")
)

// Challenge is the bounded puzzle issued to the client.
type Challenge struct {
	Challenge string `json:"challenge"`
	Salt      string `json:"salt"`
	MaxNumber int64  `json:"maxNumber"`
	Expires   int64  `json:"expires"`
	Signature string `json:"signature"`
}

// Solution is what the client submits back after solving the puzzle.
type Solution struct {
	Challenge string `json:"challenge"`
	Salt      string `json:"salt"`
	MaxNumber int64  `json:"maxNumber"`
	Expires   int64  `json:"expires"`
	Signature string `json:"signature"`
	Nonce     int64  `json:"nonce"`
}

// Manager generates and verifies proof-of-work challenges.
type Manager struct {
	secretKey []byte
	tracker   *Tracker

	mu        sync.Mutex
	usedSalts map[string]int64 // salt -> expires (Unix seconds)
}

func NewManager(secretKey []byte, tracker *Tracker) *Manager {
	if tracker == nil {
		tracker = NewTracker()
	}
	return &Manager{
		secretKey: secretKey,
		tracker:   tracker,
		usedSalts: make(map[string]int64),
	}
}

// Tracker tracks request frequencies for rate limiting and dynamic difficulty scaling.
type Tracker struct {
	mu           sync.Mutex
	ipRequests   map[string][]time.Time // IP -> timestamps within 10 min window
	siteRequests []time.Time            // All timestamps within 10 min window
	ipChallenge  map[string][]time.Time // IP -> timestamps within 1 min for challenge endpoint rate limit
}

func NewTracker() *Tracker {
	return &Tracker{
		ipRequests:  make(map[string][]time.Time),
		ipChallenge: make(map[string][]time.Time),
	}
}

// CheckChallengeRate limits the generation of challenges per IP (max 30/min).
func (t *Tracker) CheckChallengeRate(ip string, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := now.Add(-time.Minute)
	list := t.ipChallenge[ip]
	valid := list[:0]
	for _, ts := range list {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	if len(valid) >= 30 {
		t.ipChallenge[ip] = valid
		return ErrRateLimited
	}
	t.ipChallenge[ip] = append(valid, now)
	return nil
}

// RecordAttempt records a registration or challenge attempt for dynamic difficulty.
func (t *Tracker) RecordAttempt(ip string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := now.Add(-10 * time.Minute)

	// Clean site-wide history
	validSite := t.siteRequests[:0]
	for _, ts := range t.siteRequests {
		if ts.After(cutoff) {
			validSite = append(validSite, ts)
		}
	}
	t.siteRequests = append(validSite, now)

	// Clean IP history
	list := t.ipRequests[ip]
	validIP := list[:0]
	for _, ts := range list {
		if ts.After(cutoff) {
			validIP = append(validIP, ts)
		}
	}
	t.ipRequests[ip] = append(validIP, now)
}

// DetermineMaxNumber picks base or elevated difficulty based on 10-minute activity.
func (t *Tracker) DetermineMaxNumber(ip string, baseMax, elevatedMax int64, threshold int, now time.Time) int64 {
	if threshold <= 0 {
		return baseMax
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := now.Add(-10 * time.Minute)

	// Check IP attempts
	ipCount := 0
	for _, ts := range t.ipRequests[ip] {
		if ts.After(cutoff) {
			ipCount++
		}
	}

	// Check site-wide attempts
	siteCount := 0
	for _, ts := range t.siteRequests {
		if ts.After(cutoff) {
			siteCount++
		}
	}

	if ipCount >= threshold || siteCount >= threshold {
		return elevatedMax
	}
	return baseMax
}

func (m *Manager) Tracker() *Tracker {
	return m.tracker
}

// Issue generates a fresh PoW challenge.
func (m *Manager) Issue(maxNumber int64) (Challenge, error) {
	if maxNumber <= 0 {
		maxNumber = 50000
	}

	// Random 16-byte salt
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return Challenge{}, fmt.Errorf("pow: generate salt: %w", err)
	}
	salt := hex.EncodeToString(saltBytes)

	// Secret integer in [0, maxNumber]
	n, err := rand.Int(rand.Reader, big.NewInt(maxNumber+1))
	if err != nil {
		return Challenge{}, fmt.Errorf("pow: generate secret: %w", err)
	}
	secret := n.Int64()

	// challenge = SHA256(salt + secret)
	sum := sha256.Sum256([]byte(salt + strconv.FormatInt(secret, 10)))
	challenge := hex.EncodeToString(sum[:])

	// 5-minute lifespan
	expires := time.Now().Add(5 * time.Minute).Unix()

	sig := m.sign(salt, challenge, maxNumber, expires)
	return Challenge{
		Challenge: challenge,
		Salt:      salt,
		MaxNumber: maxNumber,
		Expires:   expires,
		Signature: sig,
	}, nil
}

// Verify validates a submitted solution against signature, expiration, bounds, hash, and replay.
func (m *Manager) Verify(sol *Solution) error {
	if sol == nil || sol.Challenge == "" || sol.Salt == "" || sol.Signature == "" {
		return ErrMissingSolution
	}

	now := time.Now().Unix()
	if now > sol.Expires {
		return ErrExpired
	}

	if sol.Nonce < 0 || sol.Nonce > sol.MaxNumber {
		return ErrMaxExceeded
	}

	// Verify HMAC signature over salt, challenge, maxNumber, expires
	expectedSig := m.sign(sol.Salt, sol.Challenge, sol.MaxNumber, sol.Expires)
	if !hmac.Equal([]byte(expectedSig), []byte(sol.Signature)) {
		return ErrInvalidSignature
	}

	// Verify SHA256(salt + nonce) matches challenge
	sum := sha256.Sum256([]byte(sol.Salt + strconv.FormatInt(sol.Nonce, 10)))
	computed := hex.EncodeToString(sum[:])
	if !strings.EqualFold(computed, sol.Challenge) {
		return ErrInvalidNonce
	}

	// Single-use salt guard
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredSaltsLocked(now)

	if _, exists := m.usedSalts[sol.Salt]; exists {
		return ErrReplayed
	}
	m.usedSalts[sol.Salt] = sol.Expires
	return nil
}

func (m *Manager) sign(salt, challenge string, maxNumber, expires int64) string {
	mac := hmac.New(sha256.New, m.secretKey)
	// Format is colon-separated so fields cannot smudge into each other
	msg := fmt.Sprintf("%s:%s:%d:%d", salt, challenge, maxNumber, expires)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) pruneExpiredSaltsLocked(now int64) {
	for salt, exp := range m.usedSalts {
		if now > exp {
			delete(m.usedSalts, salt)
		}
	}
}
