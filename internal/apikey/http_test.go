package apikey

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func signedIn(request *http.Request, owner string) *http.Request {
	return request.WithContext(auth.WithUser(request.Context(), user.User{ID: owner}))
}

func newChallengedHandlers(t *testing.T, store *Store, verifications *int) *Handlers {
	t.Helper()
	h := NewHandlers(store)
	h.Challenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify: func(context.Context, string, string) error {
			*verifications++
			return nil
		},
	}
	return h
}

// The challenge token is single-use, and Check spends it. A request that can
// never succeed must be refused before that point, or the owner has to solve
// the challenge again just to correct a field.
func TestCreateRefusesWhatCannotSucceedBeforeTheChallenge(t *testing.T) {
	store, owner, full := newStore(t)
	verifications := 0
	h := newChallengedHandlers(t, store, &verifications)
	h.ModelAllowed = func(_ *http.Request, modelID string) error {
		if modelID == "withdrawn" {
			return httpx.Forbidden("That model is not available to this account.")
		}
		return nil
	}

	refusals := []struct {
		name string
		body string
		want int
	}{
		{"an empty name", `{"name":"","turnstile":"t"}`, http.StatusBadRequest},
		{"an expiry already past", `{"name":"k","expires_at":1,"turnstile":"t"}`, http.StatusBadRequest},
		{"a model the account may not use", `{"name":"k","model_ids":["withdrawn"],"turnstile":"t"}`, http.StatusForbidden},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			before := verifications
			recorder := httptest.NewRecorder()
			httpx.Wrap(h.create)(recorder, signedIn(httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(tc.body)), owner))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
			if verifications != before {
				t.Fatalf("the challenge was checked %d time(s) for a request that could not succeed", verifications-before)
			}
		})
	}

	recorder := httptest.NewRecorder()
	httpx.Wrap(h.create)(recorder, signedIn(httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"k","turnstile":"t"}`)), owner))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("valid request status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if verifications != 1 {
		t.Fatalf("valid request checked the challenge %d times, want once", verifications)
	}

	// An owner already at the ceiling is refused before the challenge as well.
	// IssueModels repeats the count under the lock; this is the early answer.
	for i := 0; i < MaxPerUser; i++ {
		if _, _, err := store.IssueModels(context.Background(), full, fmt.Sprintf("full %d", i), nil, 0); err != nil {
			t.Fatalf("fill owner to the ceiling: %v", err)
		}
	}
	before := verifications
	recorder = httptest.NewRecorder()
	httpx.Wrap(h.create)(recorder, signedIn(httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"k","turnstile":"t"}`)), full))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("full owner status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
	if verifications != before {
		t.Fatalf("the challenge was checked %d time(s) for an owner at the key ceiling", verifications-before)
	}
}

// The interface words these refusals itself, from the code it is given, so
// each one has to carry a code rather than only an English sentence.
func TestRefusalsCarryCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{ErrTooMany, "too_many_keys"},
		{ErrNotFound, "key_not_found"},
		{ErrInvalidName, "key_name_invalid"},
		{ErrPastExpiry, "key_expiry_past"},
	}
	for _, tc := range cases {
		var apiErr *httpx.Error
		if !errors.As(translate(tc.err), &apiErr) || apiErr.Code != tc.code {
			t.Errorf("translate(%v) = %+v, want code %q", tc.err, apiErr, tc.code)
		}
	}
}
