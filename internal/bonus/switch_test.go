package bonus

import "testing"

// The account's switch is read by the priority order, the pool draw and the
// bars view. All three go through switchedOn, so a mode added later cannot make
// the order and the screen disagree about the same bar.
func TestSwitchedOnFollowsTheModeAndTheChoice(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name    string
		mode    Mode
		defOn   bool
		enabled *bool
		want    bool
	}{
		{"on is switched on whatever the choice", ModeOn, false, &no, true},
		{"off is switched off whatever the choice", ModeOff, true, &yes, false},
		{"only is never switched on", ModeOnly, true, &yes, false},
		{"user without a choice takes the default on", ModeUser, true, nil, true},
		{"user without a choice takes the default off", ModeUser, false, nil, false},
		{"user choice overrides a default on", ModeUser, true, &no, false},
		{"user choice overrides a default off", ModeUser, false, &yes, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := switchedOn(c.mode, c.defOn, c.enabled); got != c.want {
				t.Fatalf("switchedOn(%q, default %v, choice set %v) = %v, want %v",
					c.mode, c.defOn, c.enabled != nil, got, c.want)
			}
		})
	}
}
