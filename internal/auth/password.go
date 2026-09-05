package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// Hasher wraps Argon2id with the parameters from config and a semaphore.
//
// The semaphore is the reason this is a type rather than two functions.
// Argon2id's whole defence is that it costs memory — 19 MiB per hash at the
// default settings — and that cost is per concurrent call. Without a bound, a
// hundred simultaneous login attempts would ask for two gigabytes and take
// the process down, turning the password defence into a denial-of-service
// vector. Hashes queue instead.
type Hasher struct {
	params config.Password
	slots  chan struct{}
}

func NewHasher(params config.Password) *Hasher {
	parallel := params.MaxParallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > runtime.NumCPU()*2 {
		parallel = runtime.NumCPU() * 2
	}
	return &Hasher{params: params, slots: make(chan struct{}, parallel)}
}

const (
	MinPasswordChars = 8
	// Argon2 handles any length, but an unbounded password is an unbounded
	// amount of work to hash on an unauthenticated endpoint.
	MaxPasswordChars = 256
)

var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordChars)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d characters", MaxPasswordChars)
	ErrInvalidHash      = errors.New("auth: stored password hash is malformed")
)

func ValidatePassword(password string) error {
	switch {
	case len(password) < MinPasswordChars:
		return ErrPasswordTooShort
	case len(password) > MaxPasswordChars:
		return ErrPasswordTooLong
	default:
		return nil
	}
}

// Hash returns a PHC-format string carrying the parameters used, so a later
// change to the cost settings does not invalidate existing passwords: each
// hash is verified with whatever it was created with.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()

	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt,
		h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.KeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.Memory, h.params.Iterations, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify reports whether the password matches, and whether the stored hash
// used weaker parameters than the current configuration — the caller
// re-hashes on a successful login when it did, which is how a cost increase
// rolls out without a password reset.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (ok bool, needsRehash bool, err error) {
	parsed, err := parseHash(encoded)
	if err != nil {
		return false, false, err
	}
	if len(password) > MaxPasswordChars {
		return false, false, nil
	}
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()

	candidate := argon2.IDKey([]byte(password), parsed.salt,
		parsed.iterations, parsed.memory, parsed.parallelism, uint32(len(parsed.key)))

	// Constant time: a comparison that returns early on the first differing
	// byte leaks how much of a guess was right.
	if subtle.ConstantTimeCompare(candidate, parsed.key) != 1 {
		return false, false, nil
	}

	stale := parsed.memory < h.params.Memory ||
		parsed.iterations < h.params.Iterations ||
		uint32(len(parsed.key)) < h.params.KeyLength
	return true, stale, nil
}

// DummyVerify burns the same work as a real verification against a throwaway
// hash. The login handler calls it when no such account exists, so "unknown
// user" and "wrong password" take the same time and the endpoint cannot be
// used to enumerate accounts.
func (h *Hasher) DummyVerify(ctx context.Context, password string) {
	_, _, _ = h.Verify(ctx, h.dummy(), password)
}

// Generated once, lazily, so the cost is paid on the first failed login
// rather than at every boot.
var dummyOnce struct {
	value string
	done  bool
}

func (h *Hasher) dummy() string {
	if dummyOnce.done {
		return dummyOnce.value
	}
	value, err := h.Hash(context.Background(), "obsidian-arc-timing-equaliser")
	if err != nil {
		// Cannot happen with a fixed valid password; fall back to a shape
		// that parses so Verify still does the work.
		value = "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	dummyOnce.value = value
	dummyOnce.done = true
	return value
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() { <-h.slots }

type parsedHash struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	key         []byte
}

func parseHash(encoded string) (parsedHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return parsedHash{}, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return parsedHash{}, ErrInvalidHash
	}

	var out parsedHash
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&out.memory, &out.iterations, &out.parallelism); err != nil {
		return parsedHash{}, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return parsedHash{}, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return parsedHash{}, ErrInvalidHash
	}
	out.salt, out.key = salt, key
	return out, nil
}
