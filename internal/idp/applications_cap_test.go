package idp

import (
	"errors"
	"fmt"
	"testing"
)

// The ceiling is checked and the insert made in one transaction under the
// instance lock, so the application after the last one allowed is the one
// refused, and nothing beyond it is ever written.
func TestTheStoreRefusesTheApplicationAfterTheCeiling(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for i := range MaxApps {
		if _, _, err := f.store.CreateApp(ctx, CreateAppInput{
			Name:         fmt.Sprintf("Application %d", i),
			RedirectURIs: "https://wiki.example.com/callback",
		}); err != nil {
			t.Fatalf("application %d: %v", i, err)
		}
	}

	_, _, err := f.store.CreateApp(ctx, CreateAppInput{
		Name:         "One too many",
		RedirectURIs: "https://wiki.example.com/callback",
	})
	if !errors.Is(err, ErrTooManyApplications) {
		t.Fatalf("application %d = %v, want ErrTooManyApplications", MaxApps+1, err)
	}

	apps, err := f.store.ListApps(ctx)
	if err != nil {
		t.Fatalf("list applications: %v", err)
	}
	if len(apps) != MaxApps {
		t.Errorf("%d applications stored, want the ceiling of %d", len(apps), MaxApps)
	}
}
