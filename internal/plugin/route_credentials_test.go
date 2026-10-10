package plugin

import (
	"maps"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
)

// A backend is told who a request is for, not handed the means to be them. An
// Arc API key sent in any header a client uses for one never reaches the
// package, whichever scheme came in front of it.
func TestABackendIsNotGivenAnArcKeyTheCallerSent(t *testing.T) {
	key := apikey.TokenPrefix + "3f9c1a7e0b2d4f6a8c1e3b5d7f9a0c2e"
	sent := map[string]http.Header{
		"a bearer token":    {"Authorization": {"Bearer " + key}},
		"a bare key":        {"Authorization": {key}},
		"another scheme":    {"Authorization": {"Token " + key}},
		"the X-Api-Key one": {"X-Api-Key": {key}},
	}
	for label, header := range sent {
		if got := backendHeaders(header); len(got) != 0 {
			t.Errorf("%s reached the backend: %v", label, got)
		}
	}
}

// A package's own credentials and the rest of the request still arrive: only
// the session cookie and an Arc key are withheld.
func TestABackendStillGetsItsOwnCredentialsAndTheRestOfTheRequest(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "obsidian_session=not-for-packages")
	h.Set("Authorization", "Bearer upstream-token")
	h.Set("X-Api-Key", "upstream-key")
	h.Set("Accept", "application/json")

	want := map[string]string{
		"Authorization": "Bearer upstream-token",
		"X-Api-Key":     "upstream-key",
		"Accept":        "application/json",
	}
	if got := backendHeaders(h); !maps.Equal(got, want) {
		t.Errorf("backend headers = %v, want %v", got, want)
	}
}
