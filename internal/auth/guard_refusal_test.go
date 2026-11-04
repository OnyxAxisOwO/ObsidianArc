package auth

import (
	"errors"
	"testing"
)

// A plugin may build a refusal with only a Reason. Formatting it has to answer
// rather than dereference the missing Err, which used to panic at the first
// log line that printed it.
func TestGuardRefusalFormatsWhetherOrNotItCarriesAnErr(t *testing.T) {
	cases := []struct {
		name    string
		refusal *GuardRefusal
		want    string
	}{
		{"with an err", &GuardRefusal{Err: errors.New("refused by the risk service"), Reason: "score"}, "refused by the risk service"},
		{"reason only", &GuardRefusal{Reason: "score"}, "score"},
		{"neither", &GuardRefusal{}, "auth: guard refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.refusal.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}
