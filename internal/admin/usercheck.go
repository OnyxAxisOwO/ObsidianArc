package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usercheck"
)

var errUserCheckManagerUnavailable = errors.New("admin: usercheck manager was not wired")

type userCheckRequest struct {
	Enabled       bool     `json:"enabled"`
	ExemptDomains []string `json:"exempt_domains"`
	FailureMode   string   `json:"failure_mode"`
	APIKey        string   `json:"api_key"`
	ClearAPIKey   bool     `json:"clear_api_key"`
}

type userCheckTestRequest struct {
	Email string `json:"email"`
}

func (h *Handlers) getUserCheck(w http.ResponseWriter, _ *http.Request) error {
	if h.UserCheck == nil {
		return httpx.Internal(errUserCheckManagerUnavailable)
	}
	return httpx.WriteJSON(w, http.StatusOK, userCheckResponse(h.UserCheck.Config()))
}

func (h *Handlers) putUserCheck(w http.ResponseWriter, r *http.Request) error {
	if h.UserCheck == nil {
		return httpx.Internal(errUserCheckManagerUnavailable)
	}
	var body userCheckRequest
	if err := httpx.DecodeJSON(w, r, &body, 32*1024); err != nil {
		return err
	}
	failureMode := strings.TrimSpace(body.FailureMode)
	if failureMode == "" {
		failureMode = "reject"
	}
	if failureMode != "allow" && failureMode != "reject" {
		return httpx.BadRequestCode("usercheck_failure_mode", "Failure mode must be allow or reject.")
	}
	domains := make([]string, 0, len(body.ExemptDomains))
	for _, domain := range body.ExemptDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || strings.ContainsAny(domain, "\r\n /@") {
			return httpx.BadRequestCode("usercheck_exempt_domain", "Each exempt domain must be a domain name.")
		}
		domains = append(domains, domain)
	}
	cfg := usercheck.Config{
		Enabled:       body.Enabled,
		ExemptDomains: domains,
		FailureMode:   failureMode,
	}
	if err := h.UserCheck.Save(r.Context(), cfg, body.APIKey, body.ClearAPIKey); err != nil {
		if errors.Is(err, usercheck.ErrInvalidConfig) || errors.Is(err, usercheck.ErrNotConfigured) {
			return usercheckRefused(r.Context(), err)
		}
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, userCheckResponse(h.UserCheck.Config()))
}

func (h *Handlers) testUserCheck(w http.ResponseWriter, r *http.Request) error {
	if h.UserCheck == nil {
		return httpx.Internal(errUserCheckManagerUnavailable)
	}
	var body userCheckTestRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	email := strings.TrimSpace(body.Email)
	address, err := netmail.ParseAddress(email)
	if err != nil || address.Address != email {
		return httpx.BadRequestCode("usercheck_test_address_required", "A test email address is required.")
	}
	if !h.UserCheck.Config().APIKeySet {
		return httpx.BadRequestCode("usercheck_key_missing", "The UserCheck API key is not configured.")
	}
	claimedAt := time.Now()
	allowed, err := h.UserCheck.ClaimTest(r.Context(), claimedAt)
	if err != nil {
		return httpx.Internal(err)
	}
	if !allowed {
		return httpx.TooManyRequests("usercheck_test_cooldown", "A UserCheck test can be run once every five minutes.")
	}
	result, err := h.UserCheck.Test(r.Context(), email)
	if err != nil {
		if errors.Is(err, usercheck.ErrKeyRefused) {
			// No lookup was answered, so the corrected key can be tested at once
			// instead of after the cooldown.
			if err := h.UserCheck.ReleaseTest(r.Context(), claimedAt); err != nil {
				return httpx.Internal(err)
			}
			return httpx.BadRequestCode("usercheck_key_refused", "The UserCheck API key was refused.")
		}
		if errors.Is(err, usercheck.ErrNotConfigured) || errors.Is(err, usercheck.ErrInvalidConfig) {
			return usercheckRefused(r.Context(), err)
		}
		if errors.Is(err, usercheck.ErrUnavailable) {
			return httpx.UnavailableCode("usercheck_unavailable", "UserCheck is temporarily unavailable.")
		}
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]bool{
		"disposable": result.Disposable,
		"skipped":    result.Skipped,
	})
}

// usercheckRefused answers a save or a test the manager turned down. The
// sentinels' text carries the Go package name, so the response is worded here
// rather than copied from them. The screen maps every 400 from these routes to
// its own wording, so the browser is unaffected.
//
// An unreadable stored key gets a sentence of its own, written for the
// administrator, because entering a new key or clearing the old one is the only
// fix. That case is logged at WARN: a key the server cannot open usually means
// the instance's secret key changed, which the operator must hear about. The
// other refusals are the administrator's own input, shown back to them, and
// are logged at INFO.
func usercheckRefused(ctx context.Context, err error) error {
	if errors.Is(err, usercheck.ErrKeyUnreadable) {
		slog.WarnContext(ctx, "usercheck: request refused", "error", err)
		return httpx.BadRequestCode("usercheck_key_unreadable", "The stored UserCheck API key cannot be read. Enter a new key or clear it.")
	}
	slog.InfoContext(ctx, "usercheck: request refused", "error", err)
	if errors.Is(err, usercheck.ErrNotConfigured) {
		return httpx.BadRequestCode("usercheck_key_required", "An API key is required when email screening is enabled.")
	}
	return httpx.BadRequestCode("usercheck_settings_invalid", "The UserCheck settings are invalid. Check the API key, failure mode and exempt domains.")
}

// Keep this response explicit because Config contains a read-only key-set
// status, while the API credential itself must never leave the server.
func userCheckResponse(cfg usercheck.Config) map[string]any {
	return map[string]any{
		"enabled":        cfg.Enabled,
		"exempt_domains": cfg.ExemptDomains,
		"failure_mode":   cfg.FailureMode,
		"api_key_set":    cfg.APIKeySet,
	}
}
