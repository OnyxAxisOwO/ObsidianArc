package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// CredentialFingerprint is a value that changes when the account's password
// does and not otherwise. It is a digest of the stored hash because callers keep
// it in memory for as long as a connection lives, and the hash itself has no
// business sitting there.
//
// A rehash on sign-in, when the hashing parameters have moved on, changes the
// stored hash without changing the password, so it moves the fingerprint too.
// That ends a console connection once, which is conservative rather than wrong.
func (s *Service) CredentialFingerprint(ctx context.Context, userID string) (string, error) {
	hash, err := s.users.PasswordHash(ctx, nil, userID)
	if err != nil {
		return "", err
	}
	return fingerprintOf(hash), nil
}

// fingerprintOf is the one digest both readers use. VerifyCredential takes it
// from the hash it checked a password against, CredentialFingerprint from the
// hash it reads, so the two agree for as long as the password does.
func fingerprintOf(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:])
}
