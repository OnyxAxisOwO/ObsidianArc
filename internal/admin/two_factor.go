package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Two-step sign-in, from the operator's side: how far the instance is from
// the policy it wants, and the reset for somebody who has lost both their
// phone and their recovery codes.

// backofficeNeedsTwoFactor is the refusal every administrative endpoint gives
// an administrator the policy says must enrol first. Coded, so the backoffice
// can draw the way to enrol instead of an error.
func backofficeNeedsTwoFactor() error {
	return httpx.ForbiddenCode("two_factor_backoffice",
		"Set up two-step sign-in before using the backoffice.")
}

// backofficeLocked is the refusal while a visit to the backoffice has not
// started with a code, or has sat idle past the operator's minutes. Coded,
// so the backoffice asks for the code instead of showing an error.
func backofficeLocked() error {
	return httpx.ForbiddenCode("two_factor_backoffice_verify",
		"Enter a code from your authenticator app to open the backoffice.")
}

func (h *Handlers) twoFactorAdoption(w http.ResponseWriter, r *http.Request) error {
	adoption, err := h.auth.TwoFactorAdoption(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, adoption)
}

// resetTwoFactor switches somebody else's second step off. The account's
// owner can sign in with the password alone afterwards — and, where the
// policy requires it, is sent straight to enrol again.
//
// Held to the rule a password reset is: an administrator may do this only to
// an account they could manage, so a delegated operator cannot strip the
// second factor off a super administrator whose password they also reset.
func (h *Handlers) resetTwoFactor(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	// Somebody else's factor only, as deleteUser keeps to somebody else's
	// account: switching off one's own takes a code, and a session on its
	// own — perhaps a stolen one — must not be enough to skip that.
	if userID == actor.ID {
		return httpx.BadRequestCode("two_factor_self_reset",
			"Turn off your own two-step sign-in from your security settings; it takes a code.")
	}

	target, err := h.auth.ResetTwoFactor(r.Context(), userID, func(q database.Queryer, target user.User) error {
		fresh, err := h.users.ByID(r.Context(), q, actor.ID)
		if err != nil {
			return err
		}
		if !fresh.CanAdmin("users") || (target.IsAdmin() && !fresh.CanManageAdmin(target)) {
			return permissionDenied()
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, auth.ErrTwoFactorDisabled) {
			return httpx.Conflict("two_factor_disabled", "Two-step sign-in is not on for this account.")
		}
		return translateUserError(err)
	}

	if h.security != nil {
		if err := h.recordDetached(r.Context(), securityevents.Event{
			Event: securityevents.EventTwoFactor, Severity: securityevents.SeverityWarning,
			UserID: target.ID, Username: target.Username,
			ActorID: actor.ID, ActorUsername: actor.Username,
			IP: h.clientIP(r), Source: "admin", Decision: "reset",
		}); err != nil {
			slog.ErrorContext(r.Context(), "could not record a two-step reset", "error", err)
		}
	}
	slog.WarnContext(r.Context(), "administrator reset two-step sign-in", "actor", actor.ID, "target", userID)
	h.tellAccount(r.Context(), actor, target.ID, "two_factor_changed", "/settings?tab=security",
		map[string]any{"kind": "reset"})
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": target})
}

// checkTwoFactorSettings validates the three settings together, because the
// policy's one real hazard involves the person saving it: every level above
// optional closes the backoffice to administrators without a second step, and
// an operator who saved that without one would be shut out of the screen
// they were standing on — and out of the only screen that could undo it.
func checkTwoFactorSettings(actor user.User, body map[string]string) error {
	if policy, present := body[settings.TwoFactorPolicy]; present {
		if !settings.ValidTwoFactorPolicy(policy) {
			return httpx.BadRequest("Unknown two-step policy %q.", policy)
		}
		if policy != settings.TwoFactorOptional && !actor.TwoFactorEnabled() {
			return httpx.ForbiddenCode("two_factor_self",
				"Switch on two-step sign-in for your own account before requiring it of others.")
		}
	}
	if mode, present := body[settings.TwoFactorBackofficeMode]; present {
		if !settings.ValidBackofficeVerifyMode(mode) {
			return httpx.BadRequest("Unknown backoffice verification mode %q.", mode)
		}
		// The same hazard as the policy: a code at the backoffice's door
		// means none without a factor, including for the one saving it.
		if mode != settings.BackofficeVerifyOff && !actor.TwoFactorEnabled() {
			return httpx.ForbiddenCode("two_factor_self",
				"Switch on two-step sign-in for your own account before requiring it of others.")
		}
	}
	if issuer, present := body[settings.TwoFactorIssuer]; present {
		trimmed := strings.TrimSpace(issuer)
		// The app reads its label as "issuer:account", so a colon in the
		// issuer would move where it thinks the account name starts.
		if utf8.RuneCountInString(trimmed) > 64 || strings.Contains(trimmed, ":") {
			return httpx.BadRequest("The authenticator name must be at most 64 characters, without a colon.")
		}
	}
	return nil
}

func (h *Handlers) clientIP(r *http.Request) string {
	if h.ClientIP == nil {
		return ""
	}
	return h.ClientIP(r)
}
