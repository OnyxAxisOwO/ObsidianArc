package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// The bound on the login limiter is what an anonymous flood of invented names
// runs into. Before the change a full map refused every new name, so the flood
// locked out the owner's sign-in until the map drained. This runs the real
// sign-in path with a ceiling small enough that a short flood fills it.
func TestAFloodOfInventedNamesDoesNotLockOutOrForgiveARealAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	f.auth.limiter.ceiling = 4

	for i := 0; i < 2; i++ {
		_, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "wrong-guess", IP: "203.0.113.5"})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("a wrong password = %v, want ErrInvalidCredentials", err)
		}
	}

	// Several times the ceiling, from no address at all, so nothing but the
	// names themselves is counted.
	for i := 0; i < 12; i++ {
		_, _, err := f.auth.Login(ctx, LoginInput{Identifier: fmt.Sprintf("nobody-%d", i), Password: "whatever"})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("invented name %d = %v, want the ordinary refusal", i+1, err)
		}
	}

	entry := f.auth.limiter.buckets["id:arc"]
	if entry == nil || entry.failures != 2 {
		t.Fatalf("the account's bucket after the flood = %+v, want its two failures kept", entry)
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password", IP: "198.51.100.9"}); err != nil {
		t.Fatalf("the owner was refused after the flood: %v", err)
	}
}

// A name that matched no account must be recorded as an unknown failure on
// both sign-in paths. Recorded as a failure against an account, each invented
// name would be protected for good: none of them could be reclaimed, the map
// would pass its ceiling, and the protected count would climb with the flood.
func TestInventedNamesAreNeverProtectedOnEitherSignInPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	const ceiling = 4
	f.auth.limiter.ceiling = ceiling

	// No address, so nothing but the names is counted, and several times the
	// ceiling so the map has to reclaim on every pass.
	for i := 0; i < 3*ceiling; i++ {
		name := fmt.Sprintf("nobody-%d", i)
		if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: name, Password: "whatever"}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Login(%q) = %v, want ErrInvalidCredentials", name, err)
		}
		if _, _, err := f.auth.VerifyCredential(ctx, name, "whatever", ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("VerifyCredential(%q) = %v, want ErrInvalidCredentials", name, err)
		}
		if protected := f.auth.limiter.protected; protected != 0 {
			t.Fatalf("after %d invented names the limiter protects %d buckets, want none", i+1, protected)
		}
		if n := len(f.auth.limiter.buckets); n > ceiling {
			t.Fatalf("after %d invented names the map holds %d buckets, above its ceiling of %d", i+1, n, ceiling)
		}
	}
}
