package invite

import (
	"context"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// sameIP is how a reward notices an inviter rewarding themselves. The address an
// account signed up from is stored exactly, and a session records the exact
// address it was made from. Two sign-up addresses in one IPv6 /64 are one party,
// the way the sign-up limit counts them; a session is matched on its own address.
func TestSameIPGroupsIPv6SignupsByBlockAndMatchesSessionsExactly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inviter := signedUpFrom(t, f, "inviter", "2001:db8:1::20")
	sameBlock := signedUpFrom(t, f, "same-block", "2001:db8:1::10")
	otherBlock := signedUpFrom(t, f, "other-block", "2001:db8:2::10")

	if !sameIP(ctx, f.db, sameBlock, inviter) {
		t.Fatal("two sign-ups from one /64 were not treated as one party")
	}
	if sameIP(ctx, f.db, otherBlock, inviter) {
		t.Fatal("sign-ups from different /64 blocks were treated as one party")
	}

	// The /64 comparison cannot see a session, because a session keeps the full
	// address. An inviter still signed in on the invitee's own address is the
	// case this match exists for.
	now := time.Now().UnixMilli()
	if _, err := f.db.Exec(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at, ip) VALUES (?, ?, ?, ?, ?, ?)`,
		"inviter-session", inviter.ID, now, now+time.Hour.Milliseconds(), now, otherBlock.SignupIP); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if !sameIP(ctx, f.db, otherBlock, inviter) {
		t.Fatal("an inviter signed in from the invitee's exact address was not caught")
	}
}

// signedUpFrom opens an account from ip and reads it back the way a reward does,
// so the address the check compares is the one the database holds.
func signedUpFrom(t *testing.T, f *fixture, username, ip string) user.User {
	t.Helper()
	ctx := context.Background()
	created, err := f.users.Create(ctx, nil, user.CreateInput{
		Username: username, PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive, SignupIP: ip,
	})
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	stored, err := f.users.ByID(ctx, nil, created.ID)
	if err != nil {
		t.Fatalf("read %s: %v", username, err)
	}
	if stored.SignupIP != ip {
		t.Fatalf("stored sign-up address of %s = %q, want %q as it was seen", username, stored.SignupIP, ip)
	}
	return stored
}
