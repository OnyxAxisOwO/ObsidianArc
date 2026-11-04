package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
)

// RegistrationCaptchaMode falls back to Turnstile for a mode the server cannot
// answer for — a plugin's, once the plugin is off — so that the sign-up keeps
// a challenge. The gate used to read only the on_signup switch, which nobody
// had turned on because they had chosen another mode: the page drew the widget
// and the server checked nothing.
func TestSignupKeepsItsChallengeWhenTheChosenModeIsUnavailable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "first", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	// Wired the way server.go wires it, with Cloudflare replaced by a refusal.
	f.auth.Challenge = turnstile.Gate{
		Enabled: f.settings.SignupTurnstileOn,
		Verify:  func(context.Context, string, string) error { return turnstile.ErrFailed },
	}

	// A fresh instance has Turnstile as its stored mode and the switch off,
	// so a keyless instance is not locked out of registering.
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "default", Password: "a-good-password"}); err != nil {
		t.Fatalf("a default instance refused a registration: %v", err)
	}

	for _, stored := range []string{"risk", "from-a-newer-build"} {
		if err := f.settings.Set(ctx, settings.RegistrationCaptchaMode, stored); err != nil {
			t.Fatal(err)
		}
		_, _, err := f.auth.Register(ctx, RegisterInput{Username: "after-" + stored[:4], Password: "a-good-password"})
		if !errors.Is(err, turnstile.ErrFailed) {
			t.Errorf("stored mode %q: registration without a challenge = %v, want turnstile.ErrFailed", stored, err)
		}
	}

	// A mode the server does answer for is left alone.
	if err := f.settings.Set(ctx, settings.RegistrationCaptchaMode, settings.CaptchaModeOff); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "open", Password: "a-good-password"}); err != nil {
		t.Fatalf("mode off still asked for a challenge: %v", err)
	}
}
