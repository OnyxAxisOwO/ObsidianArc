package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/usercheck"
)

func TestRegistrationAndProfileChangesScreenNewAddresses(t *testing.T) {
	in := newInstance(t)
	var calls atomic.Int32
	in.server.auth.ScreenEmail = func(_ context.Context, address string) error {
		calls.Add(1)
		switch address {
		case "founder@temporary.example", "throwaway@temporary.example":
			return usercheck.ErrDisposable
		case "person@unavailable.example":
			return usercheck.ErrUnavailable
		default:
			return nil
		}
	}
	// The first account is the administrator who will configure the policy;
	// it cannot be locked out by an address rule on an empty instance.
	founder := in.do(http.MethodPost, "/api/auth/register", map[string]string{
		"username": "founder", "password": "a-good-password", "email": "founder@temporary.example",
	}, nil)
	if founder.Code != http.StatusCreated || calls.Load() != 0 {
		t.Fatalf("first account: %d, screening calls %d", founder.Code, calls.Load())
	}

	refused := in.do(http.MethodPost, "/api/auth/register", map[string]string{
		"username": "disposable", "password": "another-password", "email": "throwaway@temporary.example",
	}, nil)
	if refused.Code != http.StatusBadRequest || errCode(t, refused) != "disposable_email" {
		t.Fatalf("disposable registration: %d %s", refused.Code, refused.Body.String())
	}
	available := in.do(http.MethodPost, "/api/auth/register", map[string]string{
		"username": "outage", "password": "another-password", "email": "person@unavailable.example",
	}, nil)
	if available.Code != http.StatusServiceUnavailable || errCode(t, available) != "email_screening_unavailable" {
		t.Fatalf("provider outage: %d %s", available.Code, available.Body.String())
	}

	member := in.register("member", "another-password")
	before := calls.Load()
	unchanged := in.do(http.MethodPatch, "/api/profile", map[string]string{"nickname": "New nickname"}, member)
	if unchanged.Code != http.StatusOK || calls.Load() != before {
		t.Fatalf("nickname edit: %d, screening calls %d want %d", unchanged.Code, calls.Load(), before)
	}
	changed := in.do(http.MethodPatch, "/api/profile", map[string]string{"email": "throwaway@temporary.example"}, member)
	if changed.Code != http.StatusBadRequest || errCode(t, changed) != "disposable_email" {
		t.Fatalf("profile changed to disposable address: %d %s", changed.Code, changed.Body.String())
	}
}
