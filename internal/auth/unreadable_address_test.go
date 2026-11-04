package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// An address the server could not read must not switch the address limit off.
// Each name here is invented, so only the shared address budget can refuse the
// guesses; without it, no attempt is ever slowed and the loop runs to its bound.
func TestUnreadableAddressesShareOneSignInBudget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}

	refused := false
	for i := 0; i < 2*addressFreeAttempts && !refused; i++ {
		_, _, err := f.auth.VerifyCredential(ctx, fmt.Sprintf("nobody-%d", i), "whatever", "")
		var limited *RateLimitError
		switch {
		case errors.As(err, &limited):
			refused = true
		case errors.Is(err, ErrInvalidCredentials):
		default:
			t.Fatalf("guess %d = %v, want ErrInvalidCredentials or a rate limit", i, err)
		}
	}
	if !refused {
		t.Fatal("guesses from an unreadable address were never slowed: the address budget was switched off")
	}
}
