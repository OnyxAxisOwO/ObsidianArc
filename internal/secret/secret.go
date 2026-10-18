// Package secret encrypts the values that must survive a database dump:
// provider API keys, and anything else added later that is a credential
// rather than data.
//
// AES-256-GCM with a key derived from the instance secret by HKDF, one
// derivation per purpose. Deriving rather than using the configured bytes
// directly means the same secret can protect several kinds of value without
// any two of them sharing a key, and it accepts a secret of any length or
// shape (a passphrase, a hex string) rather than demanding exactly 32 bytes.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"crypto/sha256"
	"golang.org/x/crypto/hkdf"
)

// Purposes. Each gets its own derived key.
const (
	PurposeProviderKey = "obsidian-arc/provider-api-key"
	// The RSA key that signs the identity tokens other sites verify.
	// Sealed because it lives in the database rather than in the data
	// directory — two instances against one database have to sign with
	// the same key — and a database backup should not be a signing key.
	PurposeSigningKey = "obsidian-arc/oidc-signing-key"
	// The shared secret an authenticator app holds for an account. Sealed for
	// the reason a provider key is: a database dump must not be a set of
	// working second factors.
	PurposeTwoFactor = "obsidian-arc/two-factor-secret"
	// Not a Box: the key that keys the digests of recovery codes and signs
	// the remembered-browser cookie. See DeriveKey.
	PurposeTwoFactorDigest = "obsidian-arc/two-factor-digest"
	PurposeEmailCode       = "obsidian-arc/email-verification-code"
	// The key that signs proof-of-work challenges. The value is part of what
	// is issued: changing it would reject every challenge issued before an
	// upgrade.
	PurposePowChallenge = "obsidian-arc/pow-challenge"
)

var ErrDecrypt = errors.New("secret: could not decrypt (wrong key, or the value is corrupt)")

// Box seals and opens values for one purpose.
type Box struct {
	aead cipher.AEAD
}

func New(masterKey []byte, purpose string) (*Box, error) {
	derived, err := DeriveKey(masterKey, purpose)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("secret: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: new GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

// DeriveKey returns 32 bytes that belong to one purpose and nothing else.
//
// Exported for the values that are compared rather than read back — a digest
// of a recovery code, the signature on a cookie — where an AEAD is the wrong
// tool but the instance secret is still the right root. A purpose used here
// must never also be given to New.
func DeriveKey(masterKey []byte, purpose string) ([]byte, error) {
	if len(masterKey) == 0 {
		return nil, errors.New("secret: master key is empty")
	}
	derived := make([]byte, 32)
	// No salt: the master key is already high-entropy and instance-wide, and
	// a random salt would have to be stored somewhere to derive the same key
	// again. The purpose string is the domain separator.
	reader := hkdf.New(sha256.New, masterKey, nil, []byte(purpose))
	if _, err := io.ReadFull(reader, derived); err != nil {
		return nil, fmt.Errorf("secret: derive key: %w", err)
	}
	return derived, nil
}

// Seal returns nonce || ciphertext || tag, ready to store in a BLOB column.
// A fresh random nonce per call is what makes it safe to encrypt the same key
// twice.
func (b *Box) Seal(plaintext string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open reverses Seal. It returns ErrDecrypt for every failure — a wrong key,
// a truncated value, a tampered tag — because the caller can do nothing
// different for any of them, and distinguishing them tells an attacker which
// part of a forgery was wrong.
func (b *Box) Open(sealed []byte) (string, error) {
	size := b.aead.NonceSize()
	if len(sealed) < size+b.aead.Overhead() {
		return "", ErrDecrypt
	}
	plaintext, err := b.aead.Open(nil, sealed[:size], sealed[size:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plaintext), nil
}

// Digest is the hex SHA-256 of value, the form a session, a device or a
// credential is stored and looked up under. It is unkeyed on purpose: the
// inputs are already high-entropy (a cookie token, a password hash), and the
// database is meant to hold the digest rather than the value it stands for.
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Sign returns body base64url-encoded, a dot, and the HMAC-SHA256 of that
// encoding under key. It is the form a signed cookie or ticket is sent in.
// The key must come from DeriveKey with a purpose of its own: two kinds of
// signed value sharing a key could be presented as one another.
func Sign(key, body []byte) string {
	encoded := base64.RawURLEncoding.EncodeToString(body)
	return encoded + "." + tag(key, encoded)
}

// Verify reverses Sign. It reports false for anything Sign did not make under
// key: no dot, a tag that does not match, or a body that is not base64url. The
// caller answers all of them with the same refusal, so a forgery learns
// nothing about which part was wrong.
func Verify(key []byte, value string) ([]byte, bool) {
	encoded, sig, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(tag(key, encoded))) {
		return nil, false
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	return body, true
}

func tag(key []byte, encoded string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Hint is the fragment of a credential safe to show an administrator so they
// can tell two keys apart, without disclosing either. Everything before the
// last four characters becomes bullets.
func Hint(value string) string {
	runes := []rune(value)
	if len(runes) <= 4 {
		return "••••"
	}
	return "••••" + string(runes[len(runes)-4:])
}
