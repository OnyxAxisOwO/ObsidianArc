package settings

import "testing"

// A Turnstile mode with a site key saved draws the sign-up widget, so the
// server checks it whatever the switch says. Without a key nothing is drawn,
// and the switch still decides.
func TestSignupTurnstileIsCheckedWhenTheWidgetIsDrawn(t *testing.T) {
	s := serviceWithGate(map[string]bool{})

	cases := []struct {
		name string
		mode string
		key  string
		flag string
		want bool
	}{
		{"turnstile, key saved, switch off", CaptchaModeTurnstile, "0x4AAAA-site", "false", true},
		{"both, key saved, switch off", CaptchaModeBoth, "0x4AAAA-site", "false", true},
		{"turnstile, no key, switch off", CaptchaModeTurnstile, "", "false", false},
		{"proof of work, key saved, switch off", CaptchaModePoW, "0x4AAAA-site", "false", false},
		{"off, key saved, switch off", CaptchaModeOff, "0x4AAAA-site", "false", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s.values[RegistrationCaptchaMode] = tc.mode
			s.values[TurnstileSiteKey] = tc.key
			s.values[TurnstileOnSignup] = tc.flag
			if got := s.SignupTurnstileOn(); got != tc.want {
				t.Errorf("SignupTurnstileOn() = %v, want %v", got, tc.want)
			}
		})
	}
}
