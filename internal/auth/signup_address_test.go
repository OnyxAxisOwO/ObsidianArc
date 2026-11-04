package auth

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The sign-up address is what an administrator reads, and what an invite
// compares with a session's exact address. The sign-up limit groups an IPv6
// address by its /64, but the address on the account stays as it was seen.
func TestAnIPv6SignupIsStoredAsTheAddressItCameFrom(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password", IP: "203.0.113.7",
	}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")
	account, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "member", Password: "a-good-password", IP: "2001:db8:1::10",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.users.ByID(ctx, nil, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SignupIP != "2001:db8:1::10" {
		t.Fatalf("stored sign-up address = %q, want the address as it was seen", stored.SignupIP)
	}
}
