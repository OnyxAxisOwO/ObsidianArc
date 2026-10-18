package apikey

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// model_id is deprecated but still accepted, and a client that sends it must
// meet the same model check as one that sends model_ids. Removing the field is
// a change to the request shape, so this pins what it still does until that is
// decided and announced.
func TestDeprecatedModelIDMeetsTheSameModelCheckAsModelIDs(t *testing.T) {
	store, owner, _ := newStore(t)
	verifications := 0
	h := newChallengedHandlers(t, store, &verifications)
	h.ModelAllowed = func(_ *http.Request, modelID string) error {
		if modelID == "withdrawn" {
			return httpx.Forbidden("That model is not available to this account.")
		}
		return nil
	}

	for _, body := range []string{
		`{"name":"k","model_id":"withdrawn","turnstile":"t"}`,
		`{"name":"k","model_ids":["withdrawn"],"turnstile":"t"}`,
	} {
		recorder := httptest.NewRecorder()
		httpx.Wrap(h.create)(recorder, signedIn(httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(body)), owner))
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403: %s", body, recorder.Code, recorder.Body.String())
		}
	}
}
