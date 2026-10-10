package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// activityOf is what Authenticate renews: the session's last-seen time and its
// expiry, and the account's last-active stamp.
type activityOf struct {
	lastSeen, expires, lastActive int64
}

func readActivity(t *testing.T, f *fixture, token string) activityOf {
	t.Helper()
	var got activityOf
	if err := f.db.QueryRow(context.Background(),
		`SELECT s.last_seen_at, s.expires_at, u.last_active_at
		   FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?`,
		HashToken(token)).Scan(&got.lastSeen, &got.expires, &got.lastActive); err != nil {
		t.Fatal(err)
	}
	return got
}

// A watch asks Resolve before each of its runs. The person behind the stream is
// not necessarily there, so asking must not extend the sign-in or stamp the
// account as active; Authenticate, which serves a person's own request, still
// does both.
func TestResolveRenewsNothingThatAuthenticateRenews(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	// Old enough that Authenticate would touch the session and the account.
	old := time.Now().Add(-2 * time.Hour).UnixMilli()
	soon := time.Now().Add(30 * time.Minute).UnixMilli()
	if _, err := f.db.Exec(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE user_id = ?`,
		old, soon, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE users SET last_active_at = ? WHERE id = ?`, old, account.ID); err != nil {
		t.Fatal(err)
	}
	before := readActivity(t, f, token)

	got, err := f.auth.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("Resolve on a live sign-in: %v", err)
	}
	if got.ID != account.ID {
		t.Fatalf("Resolve returned account %q, want %q", got.ID, account.ID)
	}
	if after := readActivity(t, f, token); after != before {
		t.Fatalf("Resolve wrote activity: before %+v, after %+v", before, after)
	}

	if _, _, err := f.auth.Authenticate(ctx, token); err != nil {
		t.Fatalf("Authenticate on the same sign-in: %v", err)
	}
	renewed := readActivity(t, f, token)
	if renewed.lastSeen <= before.lastSeen || renewed.expires <= before.expires || renewed.lastActive <= before.lastActive {
		t.Fatalf("Authenticate did not renew the sign-in: before %+v, after %+v", before, renewed)
	}
}

func TestResolveRefusesWhatAuthenticateRefuses(t *testing.T) {
	ctx := context.Background()

	t.Run("disabled account", func(t *testing.T) {
		f := newFixture(t)
		account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
		if err != nil {
			t.Fatal(err)
		}
		disabled := user.StatusDisabled
		if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{Status: &disabled}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.auth.Resolve(ctx, token); !errors.Is(err, ErrAccountDisabled) {
			t.Fatalf("a disabled account = %v, want ErrAccountDisabled", err)
		}
	})

	t.Run("second step not yet passed", func(t *testing.T) {
		f := newFixture(t)
		enrolled(t, f, "arc")
		token := pending(t, f, "arc")
		if _, err := f.auth.Resolve(ctx, token); !errors.Is(err, ErrSignInIncomplete) {
			t.Fatalf("a pending sign-in = %v, want ErrSignInIncomplete", err)
		}
	})

	t.Run("expired session", func(t *testing.T) {
		f := newFixture(t)
		account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(ctx, `UPDATE sessions SET expires_at = ? WHERE user_id = ?`,
			time.Now().Add(-time.Minute).UnixMilli(), account.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.auth.Resolve(ctx, token); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("an expired session = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("deleted account", func(t *testing.T) {
		f := newFixture(t)
		account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.users.Delete(ctx, nil, account.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.auth.Resolve(ctx, token); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("a deleted account's session = %v, want ErrSessionNotFound", err)
		}
	})
}
