package auth

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

func TestDummyHashIsSafeUnderConcurrentUnknownLogins(t *testing.T) {
	hasher := NewHasher(testParams())
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			hasher.DummyVerify(context.Background(), "not-the-password")
		}()
	}
	close(start)
	workers.Wait()

	if hasher.dummyValue == "" {
		t.Fatal("concurrent dummy verification did not initialise the timing hash")
	}
}

// Cheap parameters: these tests exercise the encoding and the comparison, not
// the cost function, and running the production settings dozens of times
// would make the suite slow for no extra coverage.
func testParams() config.Password {
	return config.Password{
		Memory:      8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
		MaxParallel: 2,
	}
}

func TestHashAndVerify(t *testing.T) {
	hasher := NewHasher(testParams())
	ctx := context.Background()

	hash, err := hasher.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Errorf("hash is not in PHC format: %s", hash)
	}
	// The plaintext must not be recoverable from, or visible in, the stored
	// value — the failure mode of a hand-rolled "hash".
	if strings.Contains(hash, "correct horse battery") {
		t.Fatal("the password appears in its own hash")
	}

	ok, stale, err := hasher.Verify(ctx, hash, "correct horse battery")
	if err != nil || !ok {
		t.Fatalf("verify correct password: ok=%v stale=%v err=%v", ok, stale, err)
	}

	ok, _, err = hasher.Verify(ctx, hash, "correct horse batterY")
	if err != nil {
		t.Fatalf("verify wrong password: %v", err)
	}
	if ok {
		t.Fatal("a wrong password verified")
	}
}

func TestHashesAreSalted(t *testing.T) {
	hasher := NewHasher(testParams())
	ctx := context.Background()

	first, err := hasher.Hash(ctx, "same password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hasher.Hash(ctx, "same password")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two hashes of one password came out identical: the salt is not random")
	}
}

// A cost increase has to apply to existing accounts without a password reset,
// which is what the rehash signal is for.
func TestVerifyReportsWeakerParameters(t *testing.T) {
	ctx := context.Background()

	weak := testParams()
	old, err := NewHasher(weak).Hash(ctx, "a password here")
	if err != nil {
		t.Fatal(err)
	}

	strong := weak
	strong.Memory *= 4
	strong.Iterations = 3

	ok, needsRehash, err := NewHasher(strong).Verify(ctx, old, "a password here")
	if err != nil || !ok {
		t.Fatalf("verify: ok=%v err=%v", ok, err)
	}
	if !needsRehash {
		t.Error("a hash made with weaker parameters was not flagged for rehashing")
	}

	fresh, err := NewHasher(strong).Hash(ctx, "a password here")
	if err != nil {
		t.Fatal(err)
	}
	if _, stale, _ := NewHasher(strong).Verify(ctx, fresh, "a password here"); stale {
		t.Error("a current-parameter hash was flagged for rehashing")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	hasher := NewHasher(testParams())
	ctx := context.Background()

	for _, encoded := range []string{
		"",
		"not-a-hash",
		"$argon2i$v=19$m=8192,t=1,p=1$AAAA$AAAA", // wrong variant
		"$argon2id$v=16$m=8192,t=1,p=1$AAAA$AAAA", // wrong version
		"$argon2id$v=19$m=8192,t=1,p=1$!!!!$AAAA", // salt is not base64
		"$argon2id$v=19$m=8192,t=1,p=1$AAAA$",     // empty key
	} {
		if _, _, err := hasher.Verify(ctx, encoded, "whatever"); err == nil {
			t.Errorf("Verify(%q) accepted a malformed hash", encoded)
		}
	}
}

func TestPasswordLengthRules(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Error("a five-character password was accepted")
	}
	if err := ValidatePassword(strings.Repeat("a", MaxPasswordChars+1)); err == nil {
		t.Error("an over-long password was accepted")
	}
	if err := ValidatePassword(strings.Repeat("a", MinPasswordChars)); err != nil {
		t.Errorf("a minimum-length password was rejected: %v", err)
	}
}

// The minimum and maximum count characters, as the messages say. A three-character
// Chinese password is nine bytes and must fail the eight-character minimum, and a
// ninety-character one is well inside the maximum.
func TestPasswordLengthIsCountedInCharacters(t *testing.T) {
	if err := ValidatePassword("中文密"); err == nil {
		t.Error("a three-character password passed the eight-character minimum")
	}
	if err := ValidatePassword(strings.Repeat("中", MinPasswordChars)); err != nil {
		t.Errorf("an eight-character password was rejected: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("中", 90)); err != nil {
		t.Errorf("a ninety-character password was rejected: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("中", MaxPasswordChars)); err != nil {
		t.Errorf("a %d-character password was rejected: %v", MaxPasswordChars, err)
	}
	if err := ValidatePassword(strings.Repeat("中", MaxPasswordChars+1)); err == nil {
		t.Error("a password one character over the maximum was accepted")
	}
}

// A password at the character maximum is more than 256 bytes when it is CJK.
// Verify once counted bytes, so an account that Hash and ValidatePassword had
// accepted could never sign in again. The byte guard below makes the test
// discriminate: a byte-counting Verify refuses this password, so only the
// character rule lets it through.
func TestVerifyAcceptsAMaximumLengthPasswordThatIsLongerInBytes(t *testing.T) {
	hasher := NewHasher(testParams())
	ctx := context.Background()
	password := strings.Repeat("中", MaxPasswordChars)
	if len(password) <= MaxPasswordChars {
		t.Fatalf("test password is %d bytes; it must exceed the byte cap to test anything", len(password))
	}
	encoded, err := hasher.Hash(ctx, password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, _, err := hasher.Verify(ctx, encoded, password)
	if !ok || err != nil {
		t.Errorf("Verify of a %d-character password: ok=%v err=%v, want true and nil", MaxPasswordChars, ok, err)
	}
}

// Verify applies the same upper bound before any hashing, so an over-long guess
// is refused without spending Argon2 work on it.
func TestVerifyRefusesAnOverlongPasswordByCharacters(t *testing.T) {
	hasher := NewHasher(testParams())
	ctx := context.Background()
	encoded, err := hasher.Hash(ctx, strings.Repeat("中", MinPasswordChars))
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, _, err := hasher.Verify(ctx, encoded, strings.Repeat("中", MaxPasswordChars+1))
	if ok || err != nil {
		t.Errorf("Verify of an over-long password: ok=%v err=%v, want false and nil", ok, err)
	}
}
