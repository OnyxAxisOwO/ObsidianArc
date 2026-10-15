package idp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

// The identity token and the key that signs it.
//
// RS256 rather than a shared secret, because the whole value of speaking this
// protocol is that software written by somebody else can verify a token
// without being configured specially: it fetches the key set, finds the key
// the header names, and checks the signature. A symmetric algorithm would
// work and would mean every application has to be told which one to expect.
//
// The JWT is assembled here rather than by a library. It is a header, a claim
// set and one signature over the two — about forty lines — and the repository
// has three Go dependencies for reasons written down in AGENTS.md.

// Keys owns the signing key: generated once, kept in the database, sealed
// with the instance secret, and cached in memory after the first read.
type Keys struct {
	db  *database.DB
	box *secret.Box

	mu      sync.RWMutex
	private *rsa.PrivateKey
	kid     string
}

func NewKeys(db *database.DB, box *secret.Box) *Keys {
	return &Keys{db: db, box: box}
}

// The size is 2048 rather than 4096: it is what every client library and
// every hardware profile handles without thinking, and the thing being
// protected is a token that lives an hour.
const keyBits = 2048

// activeSigningKeyID is the primary key of the signing key a fresh database
// receives. The fixed id makes a second first-use insert conflict with the
// first instead of adding another active row. It does not stop rows written
// under the old random ids; load() sorts those behind this one.
const activeSigningKeyID = "active"

// signer returns the key, generating and storing one the first time anything
// needs it.
//
// Lazily, so an instance that never registers an application never spends a
// second generating a key it will not use — and so that the cost lands on one
// request rather than on every start-up.
func (k *Keys) signer(ctx context.Context) (*rsa.PrivateKey, string, error) {
	k.mu.RLock()
	cached, kid := k.private, k.kid
	k.mu.RUnlock()
	if cached != nil {
		return cached, kid, nil
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	// Another goroutine may have won the race to the lock.
	if k.private != nil {
		return k.private, k.kid, nil
	}

	if private, kid, err := k.load(ctx); err == nil {
		k.private, k.kid = private, kid
		return private, kid, nil
	} else if !database.IsNotFound(err) {
		return nil, "", err
	}

	private, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, "", fmt.Errorf("idp: generate signing key: %w", err)
	}
	kid = thumbprint(&private.PublicKey)

	block := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private),
	})
	sealed, err := k.box.Seal(string(block))
	if err != nil {
		return nil, "", fmt.Errorf("idp: seal signing key: %w", err)
	}
	// The fixed id means two instances starting together both insert, and the
	// second insert is a no-op rather than a second active row. With a random
	// id per insert both rows would survive, and which one an instance read
	// back depended on created_at, a stamp taken before the commit and so not
	// ordered the way the commits are.
	if _, err := k.db.Exec(ctx,
		`INSERT INTO oauth_signing_keys (id, kid, private_key, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (id) DO NOTHING`,
		activeSigningKeyID, kid, sealed, time.Now().UnixMilli()); err != nil {
		return nil, "", fmt.Errorf("idp: store signing key: %w", err)
	}

	// Read back rather than trusting the insert: when two instances start at
	// once only one insert lands, and both must sign with the row that is
	// stored, or each rejects what the other signed.
	stored, storedKid, err := k.load(ctx)
	if err != nil {
		return nil, "", err
	}
	k.private, k.kid = stored, storedKid
	return stored, storedKid, nil
}

func (k *Keys) load(ctx context.Context) (*rsa.PrivateKey, string, error) {
	var (
		kid    string
		sealed []byte
	)
	// The active row goes first. An instance upgraded from a version that
	// inserted random ids finds no active row and keeps the oldest one, which
	// is the key it has always signed with.
	if err := k.db.QueryRow(ctx,
		`SELECT kid, private_key FROM oauth_signing_keys
		 ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, created_at, id LIMIT 1`,
		activeSigningKeyID).
		Scan(&kid, &sealed); err != nil {
		return nil, "", err
	}
	opened, err := k.box.Open(sealed)
	if err != nil {
		return nil, "", fmt.Errorf("idp: open signing key: %w", err)
	}
	block, _ := pem.Decode([]byte(opened))
	if block == nil {
		return nil, "", fmt.Errorf("idp: stored signing key is not PEM")
	}
	private, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("idp: parse signing key: %w", err)
	}
	return private, kid, nil
}

// JWKS is the public half, in the shape a client library fetches.
func (k *Keys) JWKS(ctx context.Context) (map[string]any, error) {
	private, kid, err := k.signer(ctx)
	if err != nil {
		return nil, err
	}
	public := private.PublicKey
	return map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}},
	}, nil
}

// Claims is the identity token's payload.
type Claims struct {
	Issuer   string `json:"iss"`
	Subject  string `json:"sub"`
	Audience string `json:"aud"`
	Expiry   int64  `json:"exp"`
	IssuedAt int64  `json:"iat"`
	AuthTime int64  `json:"auth_time,omitempty"`
	Nonce    string `json:"nonce,omitempty"`

	Username      string `json:"preferred_username,omitempty"`
	Name          string `json:"name,omitempty"`
	Picture       string `json:"picture,omitempty"`
	Email         string `json:"email,omitempty"`
	EmailVerified *bool  `json:"email_verified,omitempty"`
	// The account's group here, as a list of one, because every consumer of
	// a groups claim expects a list and a scalar is the thing they all break
	// on.
	Groups []string `json:"groups,omitempty"`
	// Repeated from the token response so a client that only reads the
	// identity token still knows what it was granted.
	Scope string `json:"scope,omitempty"`
}

// Sign produces the compact JWT.
func (k *Keys) Sign(ctx context.Context, claims Claims) (string, error) {
	private, kid, err := k.signer(ctx)
	if err != nil {
		return "", err
	}

	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	signing := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("idp: sign identity token: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// thumbprint names the key.
//
// Derived from the key itself rather than random, so the same key always
// carries the same name — including after a restart, and including when two
// instances read the same row.
func thumbprint(public *rsa.PublicKey) string {
	// The RFC 7638 ordering: the members that matter, sorted, with no spaces.
	canonical := fmt.Sprintf(`{"e":"%s","kty":"RSA","n":"%s"}`,
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		base64.RawURLEncoding.EncodeToString(public.N.Bytes()))
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
