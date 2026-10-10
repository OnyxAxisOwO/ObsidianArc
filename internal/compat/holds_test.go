package compat

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The browser's holds read the session cookie and nothing else, so a key
// minted before the operator switched a policy on walks past them. These
// hold the /v1 door to the same two predicates the browser uses.

func TestAKeyOfAnAccountHeldForTwoStepIsRefusedOnEveryEndpoint(t *testing.T) {
	f := newFixture(t)
	f.handlers.MustEnrolTwoFactor = func(held user.User) bool { return held.ID == f.account.ID }

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusForbidden || errorCode(t, w) != "two_factor_enrolment_required" {
		t.Fatalf("held key on /v1/models: %d %s", w.Code, w.Body.String())
	}

	// Spending is what the hold exists to stop: nothing reaches the provider
	// and nothing reaches the ledger.
	w = f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(f.model.DisplayName))
	if w.Code != http.StatusForbidden || errorCode(t, w) != "two_factor_enrolment_required" {
		t.Fatalf("held key on chat/completions: %d %s", w.Code, w.Body.String())
	}
	if got := f.upstream.received(); got != nil {
		t.Errorf("a held key reached the provider: %v", got)
	}
	if turns := f.turns(); len(turns) != 0 {
		t.Errorf("a held key wrote %d turn records", len(turns))
	}

	// The hold belongs to the account. An administrator's key, which the
	// predicate does not hold here, still works.
	if w := f.do(t, http.MethodGet, "/v1/models", f.admin, ""); w.Code != http.StatusOK {
		t.Errorf("an unheld administrator's key: %d %s", w.Code, w.Body.String())
	}
}

func TestAnEnrolledAccountKeepsItsKey(t *testing.T) {
	f := newFixture(t)
	f.handlers.MustEnrolTwoFactor = func(user.User) bool { return false }

	if w := f.do(t, http.MethodGet, "/v1/models", f.token, ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func TestAKeyOfAnAccountHeldForOIDCWaitsForTheLink(t *testing.T) {
	f := newFixture(t)
	linked := false
	f.handlers.MustBindOIDC = func(_ context.Context, held user.User) (bool, error) {
		return held.ID == f.account.ID && !linked, nil
	}

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusForbidden || errorCode(t, w) != "oidc_binding_required" {
		t.Fatalf("unlinked key: %d %s", w.Code, w.Body.String())
	}

	linked = true
	if w := f.do(t, http.MethodGet, "/v1/models", f.token, ""); w.Code != http.StatusOK {
		t.Fatalf("linked key: %d %s", w.Code, w.Body.String())
	}
}

// The browser's binding gate admits an account when this lookup fails. A key
// gets no such choice: the hold is the thing being enforced, and the client
// can ask again. The cause must not reach the response either.
func TestAFailedBindingLookupRefusesTheKey(t *testing.T) {
	f := newFixture(t)
	f.handlers.MustBindOIDC = func(context.Context, user.User) (bool, error) {
		return false, errors.New("database is locked")
	}

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusInternalServerError || errorCode(t, w) != "internal_error" {
		t.Fatalf("status = %d %s, want a refusal rather than a pass", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "database is locked") {
		t.Errorf("the lookup's cause reached the response: %s", w.Body.String())
	}
}

// Refusals are indistinguishable by design. A held key in a group without
// API access must answer exactly as a bad key does, so the hold is checked
// after the grant.
func TestAHeldKeyInAGroupWithoutAPIAccessAnswersLikeABadKey(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.handlers.MustEnrolTwoFactor = func(user.User) bool { return true }

	baseline := f.do(t, http.MethodGet, "/v1/models", "sk-oa-not-a-real-key", "")

	denied := false
	if _, err := f.groups.Update(ctx, nil, f.openGroup.ID, group.Update{APIAccess: &denied}); err != nil {
		t.Fatal(err)
	}
	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != baseline.Code || w.Body.String() != baseline.Body.String() {
		t.Errorf("held key in a group without access answered %d %q, want the bad key's %d %q",
			w.Code, w.Body.String(), baseline.Code, baseline.Body.String())
	}
}
