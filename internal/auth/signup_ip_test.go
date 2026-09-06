package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The limit an operator sets is how many accounts one address may have made
// inside the window. The one after that is refused, and the refusal is a
// refusal rather than an invitation to wait: somebody who has just made ten
// accounts does not need to be told when to come back.
func TestOneAddressIsHeldToTheLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// The first account is the administrator and no control applies to it,
	// so the limit is tested against the ones after.
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password", IP: "203.0.113.7",
	}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")
	set(t, f, settings.SignupsPerIP, "2")
	set(t, f, settings.SignupsIPWindowMin, "60")

	for i, name := range []string{"one", "two"} {
		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: name, Password: "a-good-password", IP: "198.51.100.4",
		}); err != nil {
			t.Fatalf("account %d from a fresh address was refused: %v", i+1, err)
		}
	}

	_, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "three", Password: "a-good-password", IP: "198.51.100.4",
	})
	if !errors.Is(err, ErrSignupIPBlocked) {
		t.Fatalf("the third gave %v, want ErrSignupIPBlocked", err)
	}

	// Somebody else is not affected. This is the whole reason the limit is
	// per address: the instance-wide counter refuses everybody.
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "elsewhere", Password: "a-good-password", IP: "192.0.2.9",
	}); err != nil {
		t.Errorf("a different address was caught by another's limit: %v", err)
	}
}

// Zero is off, and has to stay off: an operator who has not set a number has
// not asked for this.
func TestWithoutALimitNobodyIsBlocked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password", IP: "203.0.113.7",
	}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")

	for i := range 6 {
		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: names[i], Password: "a-good-password", IP: "203.0.113.7",
		}); err != nil {
			t.Fatalf("account %d was refused with no limit set: %v", i+1, err)
		}
	}
}

// An address that could not be resolved is not an address. Attributing those
// to "" would put every such account in one bucket, and the first
// misconfigured proxy would lock the whole instance out of registration.
func TestAnUnknownAddressIsNeverBlocked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")
	set(t, f, settings.SignupsPerIP, "1")

	for i := range 4 {
		if _, _, err := f.auth.Register(ctx, RegisterInput{
			Username: names[i], Password: "a-good-password",
		}); err != nil {
			t.Fatalf("account %d with no address was refused: %v", i+1, err)
		}
	}
}

var names = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}

func set(t *testing.T, f *fixture, key, value string) {
	t.Helper()
	if err := f.settings.Set(context.Background(), key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}
