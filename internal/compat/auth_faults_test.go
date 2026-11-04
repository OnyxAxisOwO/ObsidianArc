package compat

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A lookup that fails for any reason but a missing row is a fault on our side.
// Answering it as a bad key sends the client to replace a key that works, and
// the operator learns nothing, because the cause is never written down.

func (f *fixture) dropAPIKeys(t *testing.T) {
	t.Helper()
	if _, err := f.db.Exec(context.Background(), `DROP TABLE api_keys`); err != nil {
		t.Fatalf("drop api_keys: %v", err)
	}
}

func TestDatabaseFailureDuringAuthIsAServerErrorNotABadKey(t *testing.T) {
	f := newFixture(t)
	f.dropAPIKeys(t)

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("a server fault carried the credential challenge %q", got)
	}
	if strings.Contains(w.Body.String(), "api_keys") {
		t.Errorf("the envelope named the table: %s", w.Body.String())
	}
}

func TestServerFaultsAreLoggedWithTheirCause(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	f := newFixture(t)
	f.dropAPIKeys(t)

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logged.String(), "api_keys") {
		t.Errorf("the cause of a 500 was not logged: %q", logged.String())
	}
}

// The binding check fails inside authenticate, and the failure is written once
// by the caller that renders it. Writing it at the check as well is what made
// every failed check appear twice in the log.
func TestAFailedOIDCBindingCheckIsLoggedOnceWithItsCause(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	f := newFixture(t)
	f.handlers.MustBindOIDC = func(context.Context, user.User) (bool, error) {
		return false, errors.New("oidc lookup unavailable")
	}

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
	faults := errorLines(logged.String())
	if len(faults) != 1 {
		t.Fatalf("the failed check was logged %d times, want once:\n%s", len(faults), logged.String())
	}
	if !strings.Contains(faults[0], "oidc lookup unavailable") {
		t.Errorf("the logged failure does not carry its cause: %s", faults[0])
	}
}

// A caller that hung up cancels the lookup it was waiting on. That is not a
// fault on this side, so the log must not report it as one.
func TestAHungUpCallerIsNotLoggedAsAFault(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil).WithContext(ctx)
	logFailure(r, internalError(context.Canceled))

	if logged.Len() != 0 {
		t.Errorf("a hang-up was logged as a fault: %s", logged.String())
	}
}
