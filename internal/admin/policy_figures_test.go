package admin

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
)

// A negative figure read as "no limit" would lift a cap set by an enclosing
// level, and a window at zero would display its figure while enforcing none.
// The zero RPM and TPM stay valid: they are the documented per-account "no
// limit", and the account-level override relies on them.
func TestPolicyFiguresThatCannotBeLimitsAreRefused(t *testing.T) {
	neg := int64(-1)
	zero := int64(0)
	negCredits := -0.5
	half := 0.5
	five := int64(5)

	cases := []struct {
		name    string
		rpm     *int64
		tpm     *int64
		windows map[quota.Window]quota.Limits
		refused bool
	}{
		{name: "negative rpm", rpm: &neg, refused: true},
		{name: "negative tpm", tpm: &neg, refused: true},
		{name: "zero rpm is the documented no-limit", rpm: &zero},
		{name: "zero tpm is the documented no-limit", tpm: &zero},
		{name: "zero window requests", windows: map[quota.Window]quota.Limits{
			quota.Window5H: {Requests: &zero},
		}, refused: true},
		{name: "zero weekly tokens", windows: map[quota.Window]quota.Limits{
			quota.WindowWeek: {Tokens: &zero},
		}, refused: true},
		{name: "negative monthly credits", windows: map[quota.Window]quota.Limits{
			quota.WindowMonth: {Credits: &negCredits},
		}, refused: true},
		{name: "a fraction of a credit is a limit", windows: map[quota.Window]quota.Limits{
			quota.Window5H: {Credits: &half},
		}},
		{name: "a blank window is no limit", windows: map[quota.Window]quota.Limits{
			quota.Window5H: {Requests: nil, Tokens: &five},
		}},
	}
	for _, tc := range cases {
		err := checkPolicyFigures(tc.rpm, tc.tpm, tc.windows)
		if tc.refused && err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
		if !tc.refused && err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
		}
	}
}
