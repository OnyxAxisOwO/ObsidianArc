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
	h := &Handlers{settings: settings.New(nil)}

	out := redactedWith(live, h.secretKeys())
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
	if redactedWith(live, h.secretKeys())[settings.TurnstileSecretKey] != "" {
		t.Error("an unset secret was masked, which reads as one being stored")
	}
}

// A credential a plugin installed while the server runs is masked as well. The
// process-wide registry does not see such a plugin, so a mask built from it would
// leave the credential in the clear; the server's own list is the one the
// response is redacted with.
func TestRedactionCoversSecretsOfPluginsInstalledAtRunTime(t *testing.T) {
	const key = "zeta.api_token"
	set := settings.New(nil)
	if err := set.AddPluginDefinitions("zeta", []settings.Definition{
		{Key: key, Secret: true, Permission: "security"},
	}, nil); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{settings: set}
	live := map[string]string{key: "a-live-token-value"}
	if got := redactedWith(live, h.secretKeys())[key]; got != secretMask {
		t.Errorf("an installed plugin's credential was returned as %q, want the mask", got)
	}
}
