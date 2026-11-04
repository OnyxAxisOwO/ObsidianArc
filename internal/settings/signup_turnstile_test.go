package settings

import "testing"

func TestSignupTurnstileFollowsTheModeTheServerFallsBackTo(t *testing.T) {
	on := map[string]bool{}
	s := serviceWithGate(on)
	if err := s.AddPluginDefinitions("zeta", nil, []string{"zetacheck"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		mode     string
		pluginOn bool
		flag     string
		want     bool
	}{
		// Today's behaviour, unchanged: the operator's switch decides.
		{"untouched instance", "", false, "", false},
		{"turnstile chosen, switch off", CaptchaModeTurnstile, false, "false", false},
		{"turnstile chosen, switch on", CaptchaModeTurnstile, false, "true", true},
		{"both chosen, switch on", CaptchaModeBoth, false, "true", true},
		{"proof of work, switch off", CaptchaModePoW, false, "false", false},
		{"off, switch off", CaptchaModeOff, false, "false", false},
		// A plugin's mode while the plugin is on is the plugin's own check.
		{"plugin mode, plugin on", "zetacheck", true, "false", false},
		// The fallback: the challenge the sign-up keeps must be checked.
		{"plugin mode, plugin off", "zetacheck", false, "false", true},
		{"mode from a newer build", "from-the-future", false, "false", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			on["zeta"] = tc.pluginOn
			delete(s.values, RegistrationCaptchaMode)
			delete(s.values, TurnstileOnSignup)
			if tc.mode != "" {
				s.values[RegistrationCaptchaMode] = tc.mode
			}
			if tc.flag != "" {
				s.values[TurnstileOnSignup] = tc.flag
			}
			if got := s.SignupTurnstileOn(); got != tc.want {
				t.Errorf("SignupTurnstileOn() = %v, want %v", got, tc.want)
			}
		})
	}
}
