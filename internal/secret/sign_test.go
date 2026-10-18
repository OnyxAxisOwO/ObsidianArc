package secret

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func signingKey(t *testing.T, secret string) []byte {
	t.Helper()
	key, err := DeriveKey([]byte(secret), "obsidian-arc/test-signing")
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	return key
}

// The digest of "abc" is the published SHA-256 test vector, so the stored form
// is pinned to the one every other reader of these rows computes.
func TestDigestIsTheHexSHA256(t *testing.T) {
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := Digest("abc"); got != want {
		t.Fatalf("Digest(abc) = %s, want %s", got, want)
	}
}

func TestSignedValueRoundTrips(t *testing.T) {
	key := signingKey(t, "an-instance-secret-of-some-length")
	body := []byte(`{"n":"payload"}`)

	got, ok := Verify(key, Sign(key, body))
	if !ok {
		t.Fatal("a value Sign made did not verify under the same key")
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("verified body = %q, want %q", got, body)
	}
}

// Every forgery is refused the same way, whichever part of it is wrong, so a
// refusal says nothing about which part that was.
func TestSignedValueRejectsEveryForgery(t *testing.T) {
	key := signingKey(t, "an-instance-secret-of-some-length")
	other := signingKey(t, "a-different-instance-secret-entirely")

	signed := Sign(key, []byte("payload"))
	encoded, tag, _ := strings.Cut(signed, ".")
	// The first character is swapped for a different one, so the forgery
	// differs from the real tag whatever the tag happens to start with.
	flipped := "A"
	if tag[:1] == "A" {
		flipped = "B"
	}

	forgeries := map[string]string{
		"signed under another key": Sign(other, []byte("payload")),
		"body changed":             base64.RawURLEncoding.EncodeToString([]byte("payloae")) + "." + tag,
		"tag changed":              encoded + "." + flipped + tag[1:],
		"no separator":             encoded,
		"body not base64url":       "!!!." + tag,
		"empty":                    "",
	}
	for name, value := range forgeries {
		if body, ok := Verify(key, value); ok {
			t.Errorf("%s: accepted with body %q", name, body)
		}
	}
}
