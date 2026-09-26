package admin

import (
	"errors"
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
		return httpx.BadRequest("Failure mode must be allow or reject.")
	}
	domains := make([]string, 0, len(body.ExemptDomains))
	for _, domain := range body.ExemptDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || strings.ContainsAny(domain, "\r\n /@") {
			return httpx.BadRequest("Each exempt domain must be a domain name.")
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
			return httpx.BadRequest("%s", err.Error())
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
		return httpx.BadRequest("A test email address is required.")
	}
	if !h.UserCheck.Config().APIKeySet {
		return httpx.BadRequest("The UserCheck API key is not configured.")
	}
	allowed, err := h.UserCheck.ClaimTest(r.Context(), time.Now())
	if err != nil {
		return httpx.Internal(err)
	}
	if !allowed {
		return httpx.TooManyRequests("usercheck_test_cooldown", "A UserCheck test can be run once every five minutes.")
	}
	result, err := h.UserCheck.Test(r.Context(), email)
	if err != nil {
		if errors.Is(err, usercheck.ErrNotConfigured) || errors.Is(err, usercheck.ErrInvalidConfig) {
			return httpx.BadRequest("%s", err.Error())
		}
		if errors.Is(err, usercheck.ErrUnavailable) {
			return httpx.Unavailable("UserCheck is temporarily unavailable.")
		}
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]bool{
		"disposable": result.Disposable,
		"skipped":    result.Skipped,
	})
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
