package admin

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/idp"
)

// An operator who reaches the instance's application ceiling needs to be told
// so. Falling through to the generic failure reads as a server fault, and
// there is nothing for them to fix on the server.
func TestTheApplicationCeilingIsARefusalWithACode(t *testing.T) {
	wrapped := fmt.Errorf("create: %w", idp.ErrTooManyApplications)
	err := applicationError(wrapped)

	var refusal *httpx.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("applicationError = %v, want an httpx refusal", err)
	}
	if refusal.Status != http.StatusConflict || refusal.Code != "too_many_applications" {
		t.Errorf("status %d code %q, want 409 too_many_applications", refusal.Status, refusal.Code)
	}
}
