package admin

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The Turnstile secret is write-only, and the point of that is the response
// body: a secret in JSON is a secret in a proxy log, a browser cache and
// wherever the operator pasted the response. It leaked out of two of the
// three endpoints that return settings — the save and the import both
// returned the live map while only the read had been masked.
func TestNoResponseCarriesTheSecret(t *testing.T) {
	const secret = "0x4AAAAAAAsecretvaluenobodyshouldsee"

	live := map[string]string{
		settings.SiteName:           "Arc",
		settings.TurnstileSiteKey:   "0x4AAAAAAApublic",
		settings.TurnstileSecretKey: secret,
	}

	out := redacted(live)
	if out[settings.TurnstileSecretKey] == secret {
		t.Error("the secret came back unmasked")
	}
	if out[settings.TurnstileSecretKey] != secretMask {
		t.Errorf("masked as %q, want the mask", out[settings.TurnstileSecretKey])
	}

	// Everything else is untouched — the site key is public by design and a
	// mask over it would leave the admin form unable to show what is set.
	if out[settings.TurnstileSiteKey] != "0x4AAAAAAApublic" || out[settings.SiteName] != "Arc" {
		t.Errorf("it masked more than the secret: %+v", out)
	}

	// A copy, not the live map: masking in place would blank the running
	// configuration and the next challenge would fail for everybody.
	if live[settings.TurnstileSecretKey] != secret {
		t.Error("redacting changed the settings it was reading")
	}

	// An unset secret stays empty rather than becoming a mask, so the form
	// can tell "nothing saved" from "something saved".
	live[settings.TurnstileSecretKey] = ""
	if redacted(live)[settings.TurnstileSecretKey] != "" {
		t.Error("an unset secret was masked, which reads as one being stored")
	}
}
