package secret

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSealAndOpen(t *testing.T) {
	box, err := New([]byte("an-instance-secret-of-some-length"), PurposeProviderKey)
	if err != nil {
		t.Fatalf("new box: %v", err)
	}

	const key = "sk-ant-api03-not-a-real-key"
	sealed, err := box.Seal(key)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// The stored bytes must not contain the plaintext: the whole point is
	// that a database dump is not a set of working API keys.
	if bytes.Contains(sealed, []byte(key)) {
		t.Fatal("the ciphertext contains the plaintext")
	}

	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if opened != key {
		t.Errorf("round-trip changed the value: %q", opened)
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	box, _ := New([]byte("an-instance-secret-of-some-length"), PurposeProviderKey)

	first, _ := box.Seal("same-key")
	second, _ := box.Seal("same-key")
	if bytes.Equal(first, second) {
		t.Fatal("two seals of one value produced identical ciphertext: the nonce is not random")
	}
}

// A rotated OBSIDIAN_SECRET_KEY must fail closed rather than return garbage.
func TestOpenWithWrongKeyFails(t *testing.T) {
	original, _ := New([]byte("the-original-instance-secret-xx"), PurposeProviderKey)
	rotated, _ := New([]byte("a-different-instance-secret-yy"), PurposeProviderKey)

	sealed, _ := original.Seal("sk-secret")
	if _, err := rotated.Open(sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
}

// Two purposes derive two keys, so a value sealed for one cannot be read by
// the other even though both come from the same instance secret.
func TestPurposesAreSeparated(t *testing.T) {
	master := []byte("one-instance-secret-for-both-xx")
	keys, _ := New(master, PurposeProviderKey)
	other, _ := New(master, "obsidian-arc/something-else")

	sealed, _ := keys.Seal("sk-secret")
	if _, err := other.Open(sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("a value crossed a purpose boundary: %v", err)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	box, _ := New([]byte("an-instance-secret-of-some-length"), PurposeProviderKey)
	sealed, _ := box.Seal("sk-secret")

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := box.Open(tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatal("a modified ciphertext was accepted")
	}

	if _, err := box.Open(sealed[:4]); !errors.Is(err, ErrDecrypt) {
		t.Fatal("a truncated ciphertext was accepted")
	}
}

func TestHintRevealsOnlyTheTail(t *testing.T) {
	const key = "sk-ant-api03-abcdefgh1234"
	hint := Hint(key)

	if hint != "••••1234" {
		t.Errorf("hint = %q, want the last four characters only", hint)
	}
	// Everything before those four must be gone, or the "hint" is the key.
	if strings.Contains(hint, key[:len(key)-4]) {
		t.Errorf("hint %q exposes more than the tail", hint)
	}
	// A short value must not become its own hint.
	if Hint("abc") != "••••" {
		t.Errorf("short key hint = %q", Hint("abc"))
	}
}

func TestEmptyMasterKeyIsRejected(t *testing.T) {
	if _, err := New(nil, PurposeProviderKey); err == nil {
		t.Fatal("an empty master key was accepted")
	}
}
