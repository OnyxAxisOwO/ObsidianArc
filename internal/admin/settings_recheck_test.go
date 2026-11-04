package admin

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A plain settings save must not take the instance lock. Only a save that
// changes a second-step switch re-reads the saver's row under it, so the lock
// is not queued behind every registration and demotion for an ordinary save.
func TestOnlyASecondStepSwitchRechecksTheSaver(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{"policy", map[string]string{settings.TwoFactorPolicy: settings.TwoFactorOptional}, true},
		{"backoffice mode", map[string]string{settings.TwoFactorBackofficeMode: settings.BackofficeVerifyOff}, true},
		{"issuer alone", map[string]string{settings.TwoFactorIssuer: "Arc"}, false},
		{"unrelated setting", map[string]string{settings.LandingMode: "home"}, false},
		{"nothing", map[string]string{}, false},
	}
	for _, tc := range cases {
		if got := twoFactorRecheckNeeded(tc.values); got != tc.want {
			t.Errorf("%s: twoFactorRecheckNeeded = %v, want %v", tc.name, got, tc.want)
		}
	}
}
