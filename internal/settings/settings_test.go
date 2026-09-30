package settings

import "testing"

// The manifest's colours have to be a form CSS actually accepts and the
// server itself never repaints: an invalid one would only be noticed when a
// browser silently ignored the whole manifest field.
func TestValidHexColor(t *testing.T) {
	cases := map[string]bool{
		"#18181b":  true,
		"#FFFFFF":  true,
		"":         false,
		"18181b":   false,
		"#fff":     false, // three-digit CSS shorthand: legal CSS, refused here on purpose
		"#gggggg":  false,
		"#18181bx": false,
		"red":      false,
	}
	for value, want := range cases {
		if got := ValidHexColor(value); got != want {
			t.Errorf("ValidHexColor(%q) = %v, want %v", value, got, want)
		}
	}
}

// Mirrors web/src/lib/account.ts's safeAvatar: only a root-relative path or a
// data URI survive the image policy (`img-src 'self' data: blob:`), and the
// two checks have to agree or one of them is wrong about what will render.
func TestValidPWAIconURL(t *testing.T) {
	cases := map[string]bool{
		"":                                   true,
		"/icon.png":                          true,
		"/icons/512.svg":                     true,
		"//evil.example.com/icon.png":        false,
		"https://example.com/icon.png":       false,
		"data:image/png;base64,AAAA":         true,
		"data:image/svg+xml;base64,AAAA==":   true,
		"data:text/html;base64,PHNjcmlwdD4=": false,
		"javascript:alert(1)":                false,
	}
	for value, want := range cases {
		if got := ValidPWAIconURL(value); got != want {
			t.Errorf("ValidPWAIconURL(%q) = %v, want %v", value, got, want)
		}
	}
}

// BrowserTitle is what both the shell's own index.html and /api/site resolve
// against; a Service built without a database exercises exactly the fallback
// logic without needing one, since Get and Defaults never touch it.
func TestBrowserTitleFallsBackToSiteName(t *testing.T) {
	s := &Service{values: map[string]string{}}

	if got, want := s.BrowserTitle(), Defaults[SiteName]; got != want {
		t.Errorf("with nothing configured, BrowserTitle() = %q, want the default site name %q", got, want)
	}

	s.values[SiteName] = "My Instance"
	if got := s.BrowserTitle(); got != "My Instance" {
		t.Errorf("BrowserTitle() = %q, want the configured site name", got)
	}

	s.values[SiteBrowserTitle] = "Custom Tab Title"
	if got := s.BrowserTitle(); got != "Custom Tab Title" {
		t.Errorf("BrowserTitle() = %q, want the explicit browser title to win", got)
	}
}

// A row the admin form would never have accepted — an import, an older build,
// a hand edit — must not leave every account looking at a blank figure: the
// allowance and the bonus bars both word themselves by this.
func TestUsageDisplayFallsBackToTheFiguresWhenTheStoredValueIsNotOneOfTheThree(t *testing.T) {
	s := &Service{values: map[string]string{}}
	if got := s.UsageDisplay(); got != UsageAbsolute {
		t.Errorf("with nothing configured, UsageDisplay() = %q, want the figures", got)
	}
	for _, want := range UsageDisplays {
		s.values[UsageDisplay] = want
		if got := s.UsageDisplay(); got != want {
			t.Errorf("UsageDisplay() = %q, want %q", got, want)
		}
	}
	for _, bad := range []string{"", "percent", "REMAINING", " remaining"} {
		s.values[UsageDisplay] = bad
		if got := s.UsageDisplay(); got != UsageAbsolute {
			t.Errorf("a stored %q gave %q, want the figures", bad, got)
		}
	}
}

// The front door's modes are a closed set on both sides: the frontend's
// `Landing.mode` union names the same four, and admin.instance refuses a save
// that is not one of them. A mode added to one side and not the other is a
// front door the server accepts and the browser cannot draw — so the count is
// asserted, not just the membership.
func TestValidLandingMode(t *testing.T) {
	cases := map[string]bool{
		LandingLogin:     true,
		LandingIntroPage: true,
		LandingChat:      true,
		LandingSite:      true,
		"":               false,
		"Login":          false, // the values are compared exactly, never folded
		"marketing":      false,
		"site ":          false,
	}
	for value, want := range cases {
		if got := ValidLandingMode(value); got != want {
			t.Errorf("ValidLandingMode(%q) = %v, want %v", value, got, want)
		}
	}

	if len(LandingModes) != 4 {
		t.Errorf("LandingModes has %d entries, want 4 — a mode added here needs the "+
			"same entry in web/src/api/auth.ts's Landing union and an option in "+
			"AdminSettings, or an operator can save a front door nothing draws",
			len(LandingModes))
	}
}

// The default has to stay the sign-in card. Every other mode publishes
// something to people with no account, and an upgrade must not make that
// decision on an operator's behalf.
func TestLandingDefaultsToTheSignInCard(t *testing.T) {
	if got := Defaults[LandingMode]; got != LandingLogin {
		t.Errorf("Defaults[LandingMode] = %q, want %q", got, LandingLogin)
	}
}
