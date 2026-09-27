package auth

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The transport for two-step sign-in: the second step of signing in, and the
// four things an account does to its own second factor.

type codeRequest struct {
	Code string `json:"code"`
	// Only read by the sign-in step: whether this browser may skip the code
	// next time, where the operator allows that at all.
	Remember bool `json:"remember"`
}

// completeSignIn is the second step. It is public for the reason /login is:
// the caller has no session yet, only the pending one the password bought,
// which it presents as the cookie.
func (h *Handlers) completeSignIn(w http.ResponseWriter, r *http.Request) error {
	var body codeRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	account, token, err := h.service.CompleteSignIn(r.Context(), h.service.TokenFrom(r), body.Code,
		httpx.ClientIP(r, h.trust), r.UserAgent())
	if err != nil {
		if errors.Is(err, ErrNoPendingSignIn) {
			// Expired, or never started: the cookie is worth nothing now.
			h.service.ClearCookie(w)
		}
		return twoFactorError(w, err)
	}
	h.service.SetCookie(w, token)
	h.service.AttachDevice(r.Context(), w, r, account, token, httpx.ClientIP(r, h.trust), r.UserAgent())
	if body.Remember {
		h.service.Remember(w, account)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": h.account(r, account)})
}

// signInPending answers a request that is halfway signed in. Coded, so the
// client asks for the code rather than for the password again.
func signInPending() error {
	return httpx.UnauthorizedCode("two_factor_pending",
		"Enter the code from your authenticator app to finish signing in.")
}

func (h *Handlers) twoFactorStatus(w http.ResponseWriter, r *http.Request) error {
	status, err := h.service.TwoFactorStatus(r.Context(), MustUser(r.Context()))
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, status)
}

func (h *Handlers) beginTwoFactor(w http.ResponseWriter, r *http.Request) error {
	setup, err := h.service.BeginTwoFactor(r.Context(), MustUser(r.Context()))
	if err != nil {
		return twoFactorError(w, err)
	}
	return httpx.WriteJSON(w, http.StatusOK, setup)
}

func (h *Handlers) enableTwoFactor(w http.ResponseWriter, r *http.Request) error {
	account := MustUser(r.Context())
	session, _ := SessionFrom(r.Context())
	var body codeRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	ip, userAgent := clientOf(r, h.trust)
	codes, updated, err := h.service.EnableTwoFactor(r.Context(), account.ID, body.Code, session.ID,
		ip, userAgent)
	if err != nil {
		return twoFactorError(w, err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"recovery_codes": codes,
		"user":           h.account(r, updated),
	})
}

func (h *Handlers) disableTwoFactor(w http.ResponseWriter, r *http.Request) error {
	var body codeRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	updated, err := h.service.DisableTwoFactor(r.Context(), MustUser(r.Context()), body.Code,
		httpx.ClientIP(r, h.trust))
	if err != nil {
		return twoFactorError(w, err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": h.account(r, updated)})
}

func (h *Handlers) regenerateRecovery(w http.ResponseWriter, r *http.Request) error {
	var body codeRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	codes, err := h.service.RegenerateRecovery(r.Context(), MustUser(r.Context()), body.Code,
		httpx.ClientIP(r, h.trust))
	if err != nil {
		return twoFactorError(w, err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// enterBackoffice takes the code a visit to the backoffice starts with, for
// whatever is holding the visit: this browser session, or — dispatched from
// the SSH console — that connection.
func (h *Handlers) enterBackoffice(w http.ResponseWriter, r *http.Request) error {
	var body codeRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	// The browser's address and agent, not the dispatched request's: typed
	// into the web terminal, this arrives from inside the process.
	ip, userAgent := clientOf(r, h.trust)
	err := h.service.EnterBackoffice(r.Context(), MustUser(r.Context()), body.Code, ip, userAgent)
	if errors.Is(err, ErrNoBackofficeVisit) {
		return httpx.BadRequest("There is nothing here to unlock the backoffice for.")
	}
	if err != nil {
		return twoFactorError(w, err)
	}
	return httpx.NoContent(w)
}

// leaveBackoffice ends the visit. Sent as the backoffice unmounts and as the
// page goes away, by a beacon that cannot read an answer, so it has none
// worth reading and never fails for want of a visit to end.
func (h *Handlers) leaveBackoffice(w http.ResponseWriter, r *http.Request) error {
	if session, ok := SessionFrom(r.Context()); ok {
		if err := h.service.LeaveBackoffice(r.Context(), session); err != nil {
			return httpx.Internal(err)
		}
	}
	return httpx.NoContent(w)
}

// TranslateTwoFactorError words every refusal with a code, because each one is said
// in the reader's own language by the screen that asked.
func TranslateTwoFactorError(w http.ResponseWriter, err error) error {
	var limited *RateLimitError
	if errors.As(err, &limited) {
		seconds := int(limited.RetryAfter.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		return httpx.TooManyRequests("too_many_attempts", limited.Error()).
			WithDetails(map[string]any{"retry_after_seconds": seconds})
	}
	switch {
	case errors.Is(err, ErrTwoFactorCode):
		return httpx.BadRequestCode("two_factor_code", "That code is not valid.")
	case errors.Is(err, ErrNoPendingSignIn):
		return httpx.UnauthorizedCode("two_factor_expired",
			"That sign-in has expired. Enter your password again.")
	case errors.Is(err, ErrAccountDisabled):
		return httpx.ForbiddenCode("account_banned", "This account has been banned. Contact an administrator.")
	case errors.Is(err, ErrTwoFactorEnabled):
		return httpx.Conflict("two_factor_enabled", "Two-step sign-in is already on.")
	case errors.Is(err, ErrTwoFactorDisabled):
		return httpx.Conflict("two_factor_disabled", "Two-step sign-in is not on.")
	case errors.Is(err, ErrTwoFactorNoSetup):
		return httpx.BadRequestCode("two_factor_no_setup",
			"That setup has expired. Start again to get a new QR code.")
	case errors.Is(err, ErrTwoFactorMandatory):
		return httpx.ForbiddenCode("two_factor_mandatory",
			"This server requires two-step sign-in for your account.")
	case errors.Is(err, ErrTwoFactorUnavailable):
		return httpx.UnavailableCode("two_factor_unavailable",
			"Two-step sign-in is not available on this server.")
	case errors.Is(err, secret.ErrDecrypt):
		// The instance secret changed under a stored factor. Nothing the
		// person can do fixes it; an administrator can reset the factor.
		return httpx.Internal(err)
	case errors.Is(err, user.ErrNotFound):
		return httpx.NotFound("No such account.")
	default:
		return httpx.Internal(err)
	}
}

func twoFactorError(w http.ResponseWriter, err error) error {
	return TranslateTwoFactorError(w, err)
}
