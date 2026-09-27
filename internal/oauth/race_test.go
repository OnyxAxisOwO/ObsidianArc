package oauth

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A queue of people arriving at once is the normal case at the start of a
// term and the worst case at the top of the hour: every goroutine here is a
// real sign-in, not a simulation, and what must hold is that no two of them
// open the same door twice.

// The same identity, arriving N times at once: one account, linked once, and
// every arrival walks out with the same account.
func TestConcurrentArrivalsOfOneIdentityOpenOneAccount(t *testing.T) {
	f := newFixture(t)
	populate(t, f)

	const arrivals = 16
	var (
		wg  sync.WaitGroup
		ids = make([]string, arrivals)
		err = make([]error, arrivals)
	)
	for i := 0; i < arrivals; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			account, signErr := f.service.SignIn(context.Background(),
				Identity{Provider: "oidc", Subject: "12345678", Login: "qq_12345678"},
				"203.0.113.5", "a browser")
			ids[slot], err[slot] = account.ID, signErr
		}(i)
	}
	wg.Wait()

	for slot, signErr := range err {
		if signErr != nil {
			t.Fatalf("arrival %d: %v", slot, signErr)
		}
		if ids[slot] == "" {
			t.Fatalf("arrival %d returned no account", slot)
		}
		if ids[slot] != ids[0] {
			t.Fatalf("arrival %d reached %q, want the one account %q", slot, ids[slot], ids[0])
		}
	}
	if total, _ := f.users.Count(context.Background(), nil); total != 1 {
		t.Errorf("accounts = %d, want only the founder", total)
	}
}

// Distinct identities whose subjects are distinct QQ numbers, arriving at
// once on an instance that allows signups: one account each, and the unique
// index plus the instance lock keep any two of them from claiming the same
// number.
func TestConcurrentSignupsClaimDistinctQQNumbers(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	if err := f.settings.Set(context.Background(), settings.OAuthAllowSignup, "true"); err != nil {
		t.Fatalf("allow signups: %v", err)
	}

	const arrivals = 12
	var (
		wg  sync.WaitGroup
		qqs = make([]string, arrivals)
		err = make([]error, arrivals)
	)
	for i := 0; i < arrivals; i++ {
		subject := "90000000" + strconv.Itoa(i)
		wg.Add(1)
		go func(slot int, sub string) {
			defer wg.Done()
			account, signErr := f.service.SignIn(context.Background(),
				Identity{Provider: "oidc", Subject: sub, Login: "qq_" + sub},
				"203.0.113.5", "a browser")
			qqs[slot], err[slot] = account.QQ, signErr
		}(i, subject)
	}
	wg.Wait()

	seen := map[string]bool{}
	for slot, signErr := range err {
		if signErr != nil {
			t.Fatalf("arrival %d: %v", slot, signErr)
		}
		if seen[qqs[slot]] {
			t.Errorf("arrival %d holds QQ %q, already claimed", slot, qqs[slot])
		}
		seen[qqs[slot]] = true
	}
	if total, _ := f.users.Count(context.Background(), nil); total != 1+arrivals {
		t.Errorf("accounts = %d, want the founder plus %d", total, arrivals)
	}
}

// Two accounts binding the same number from the settings screen at once: the
// account row lock serializes them, and the loser keeps whatever it had
// rather than stealing the number mid-flight.
func TestConcurrentConnectsToTheSameNumberLeaveOneWinner(t *testing.T) {
	f := newFixture(t)
	populate(t, f)

	first, err := f.service.SignIn(context.Background(),
		Identity{Provider: "github", Subject: "4218", Login: "octocat", Email: "cat@example.com"},
		"203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	second, err := f.service.SignIn(context.Background(),
		Identity{Provider: "github", Subject: "4219", Login: "octocat2", Email: "cat2@example.com"},
		"203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}

	var (
		wg   sync.WaitGroup
		errs = make([]error, 2)
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = f.service.Connect(context.Background(), first.ID,
			Identity{Provider: "oidc", Subject: "55512345", Login: "qq_55512345"})
	}()
	go func() {
		defer wg.Done()
		errs[1] = f.service.Connect(context.Background(), second.ID,
			Identity{Provider: "oidc", Subject: "55512345", Login: "qq_55512345"})
	}()
	wg.Wait()

	// One account wins the identity; the other is told it is spoken for, and
	// its QQ write never happens because Connect refused before it.
	linked, refused := 0, 0
	for _, connectErr := range errs {
		switch {
		case connectErr == nil:
			linked++
		case errors.Is(connectErr, ErrAlreadyLinked):
			refused++
		default:
			t.Fatalf("connect: %v", connectErr)
		}
	}
	if linked != 1 || refused != 1 {
		t.Errorf("connects = %d linked / %d refused, want exactly one of each", linked, refused)
	}
	carriers := 0
	for _, accountID := range []string{first.ID, second.ID} {
		account, readErr := f.users.ByID(context.Background(), nil, accountID)
		if readErr != nil {
			t.Fatalf("read %s: %v", accountID, readErr)
		}
		if account.QQ == "55512345" {
			carriers++
		}
	}
	if carriers != 1 {
		t.Errorf("accounts carrying %q = %d, want exactly one", "55512345", carriers)
	}
}
