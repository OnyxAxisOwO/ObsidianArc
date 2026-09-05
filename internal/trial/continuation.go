package trial

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Making the turn limit true.
//
// The trial stores nothing, so the exchange arrives in the request body — and
// counting the turns in that body is counting a number the client wrote. A
// visitor who sends only their latest question looks like turn one, every
// time, and the cap means nothing.
//
// So the count is handed back signed. The client returns it with the next
// question, the signature is checked against the instance secret, and a value
// that was edited or minted elsewhere is simply not believed. Nothing is
// stored on this side: the token is the state, and it is unforgeable rather
// than trusted.
//
// This makes the limit real for a client that participates. Someone who
// discards the token starts a fresh trial each time, which is why it is not
// the only bound — the per-address budget and the instance-wide ceiling in
// trial.go are what hold when the token is thrown away.

const continuationTTL = 2 * time.Hour

var errContinuation = errors.New("trial: continuation token is not valid")

type signer struct{ key []byte }

func newSigner(secret []byte) *signer {
	// Derived rather than used raw, so a token cannot be confused with
	// anything else signed by the same instance secret.
	sum := sha256.Sum256(append([]byte("obsidian-arc/trial-continuation\x00"), secret...))
	return &signer{key: sum[:]}
}

// issue returns the token to hand back after a turn.
func (s *signer) issue(turns int, now time.Time) string {
	body := fmt.Sprintf("%d.%d", turns, now.Add(continuationTTL).UnixMilli())
	return body + "." + s.tag(body)
}

// verify reads a token and returns the turns already used. An absent token is
// a new trial rather than an error: someone opening the page for the first
// time has none, and there is no way to tell that apart from someone who
// discarded one.
func (s *signer) verify(token string) (int, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return 0, nil
	}

	turnsPart, rest, ok := strings.Cut(trimmed, ".")
	if !ok {
		return 0, errContinuation
	}
	expiryPart, tag, ok := strings.Cut(rest, ".")
	if !ok {
		return 0, errContinuation
	}

	body := turnsPart + "." + expiryPart
	if !hmac.Equal([]byte(tag), []byte(s.tag(body))) {
		return 0, errContinuation
	}

	turns, err := strconv.Atoi(turnsPart)
	if err != nil || turns < 0 {
		return 0, errContinuation
	}
	expiry, err := strconv.ParseInt(expiryPart, 10, 64)
	if err != nil {
		return 0, errContinuation
	}
	// An expired token is a finished trial, not a fresh one: treating it as
	// zero would make waiting two hours the way around the limit.
	if time.Now().UnixMilli() > expiry {
		return 0, errContinuation
	}
	return turns, nil
}

func (s *signer) tag(body string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
