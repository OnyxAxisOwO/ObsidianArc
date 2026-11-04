package invite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A regeneration refused for being too frequent tells the client how long to
// wait in two places, the Retry-After header and the body, and both have to be
// the same number. Claims and regenerations share the code that writes them.
func TestRegenerateRefusalCarriesTheWaitInHeaderAndBody(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	handlers := NewHandlers(f.store)
	inviter := f.account(t, "regen-throttled")

	if err := f.store.settings.SetMany(ctx, map[string]string{settings.InvitesUserEnabled: "true"}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	for i := 0; i < maxRegenerates; i++ {
		if _, err := f.store.Regenerate(ctx, inviter.ID); err != nil {
			t.Fatalf("regenerate %d: %v", i+1, err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/profile/invites/regenerate", nil)
	req = req.WithContext(auth.WithUser(req.Context(), inviter))
	recorder := httptest.NewRecorder()
	err := handlers.regenerate(recorder, req)

	var httpxErr *httpx.Error
	if !errors.As(err, &httpxErr) || httpxErr.Status != http.StatusTooManyRequests {
		t.Fatalf("the eleventh regeneration gave %v, want 429", err)
	}
	header := recorder.Header().Get("Retry-After")
	seconds, convErr := strconv.Atoi(header)
	if convErr != nil || seconds < 1 {
		t.Fatalf("Retry-After = %q, want a positive whole number of seconds", header)
	}
	if got, _ := httpxErr.Details["retry_after_seconds"].(int); got != seconds {
		t.Fatalf("body says %v seconds, header says %d", httpxErr.Details["retry_after_seconds"], seconds)
	}
}
