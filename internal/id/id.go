// Package id generates the identifiers used for every row in the database.
//
// They are ULIDs: 48 bits of millisecond timestamp followed by 80 bits of
// randomness, rendered as 26 Crockford base32 characters. Two properties are
// why, and both matter here:
//
//   - They sort lexicographically in creation order, so `ORDER BY id` is
//     chronological, a B-tree index on the primary key stays append-friendly,
//     and pagination needs no second column.
//   - They are unguessable, so an identifier appearing in a URL tells an
//     attacker nothing about how many rows exist or what the next one is —
//     the failure mode that makes IDOR bugs catastrophic with integer keys.
package id

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"time"
)

// Crockford base32: no I, L, O or U, so a transcribed identifier cannot turn
// into a different valid one.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Length is the character count of every identifier this package produces.
const Length = 26

// New returns a fresh identifier for the current time.
func New() string { return NewAt(time.Now()) }

// NewAt returns an identifier whose sort position matches t. Exposed so a
// backfill or an import can produce ids that order correctly against rows
// created earlier.
func NewAt(t time.Time) string {
	var raw [16]byte
	// 48-bit big-endian millisecond timestamp: the high bytes of a uint64,
	// which is what makes byte order and time order the same thing.
	binary.BigEndian.PutUint64(raw[:8], uint64(t.UnixMilli())<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		// crypto/rand does not fail on any supported platform; if it somehow
		// did, continuing with predictable identifiers would be worse than
		// stopping.
		panic("id: crypto/rand unavailable: " + err.Error())
	}
	return encode(raw)
}

// 128 bits do not divide into 5-bit groups, so the encoder starts with two
// zero bits of padding and emits exactly 26 characters.
func encode(raw [16]byte) string {
	out := make([]byte, 0, Length)
	acc := uint32(0)
	bits := uint(2)
	for _, b := range raw {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, alphabet[(acc>>bits)&31])
		}
	}
	return string(out)
}

// Valid reports whether s could have been produced by this package. Used to
// reject a malformed path parameter before it reaches a query.
func Valid(s string) bool {
	if len(s) != Length {
		return false
	}
	for i := 0; i < len(s); i++ {
		if indexInAlphabet(s[i]) < 0 {
			return false
		}
	}
	return true
}

func indexInAlphabet(c byte) int {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return i
		}
	}
	return -1
}

// Secret returns n cryptographically random bytes as an unpadded base64url
// string. Session tokens and similar bearer values come from here rather than
// from New: they must not be sortable, and they must not leak a timestamp.
func Secret(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("id: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
