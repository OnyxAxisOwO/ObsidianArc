// The backoffice's departure actions: processing a member leaving the
// community's group chat, and reading back the ones already processed.
// The heavy lifting is invite.Store.Depart; what lives here is the guards
// the operator's own surface needs — you cannot process yourself, and the
// response says how much of the inviter's reward was actually clawed back,
// because "due" and "taken" are different numbers an operator reads.
package admin

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type departBody struct {
	Mode string `json:"mode"`
	Note string `json:"note"`
}

func (h *Handlers) departUser(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if userID == actor.ID {
		return httpx.BadRequest("You cannot process your own departure.")
	}

	var body departBody
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	mode := body.Mode
	if !settings.ValidDepartMode(mode) {
		return httpx.BadRequestCode("departure_mode", "Mode must be %q or %q.",
			settings.DepartModeDisable, settings.DepartModeDelete)
	}

	result, err := h.invites.Depart(r.Context(), invite.DepartInput{
		UserID:    userID,
		Mode:      mode,
		Source:    invite.SourceAdmin,
		ActorID:   actor.ID,
		ActorName: actor.Username,
		Note:      body.Note,
		IP:        h.clientIP(r),
	})
	if err != nil {
		switch {
		case errors.Is(err, invite.ErrDeparted):
			return httpx.Conflict("already_departed",
				"This account has already been processed for a departure.")
		case errors.Is(err, invite.ErrDepartAdmin):
			return httpx.Conflict("departure_refused",
				"An administrator account can only be processed by an administrator, and never by the bot.")
		case errors.Is(err, invite.ErrDepartLastAdmin):
			return httpx.Conflict("last_admin", "This is the last super administrator; promote someone else first.")
		case errors.Is(err, user.ErrNotFound):
			return translateUserError(err)
		default:
			return httpx.Internal(err)
		}
	}

	slog.WarnContext(r.Context(), "administrator processed a group departure",
		"actor", actor.ID, "target", userID, "mode", mode,
		"cards_due", result.RewardCardsDue, "cards_revoked", result.CardsRevoked)

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"departure": result})
}

func (h *Handlers) listDepartures(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	departures, total, err := h.invites.ListDepartures(r.Context(),
		intParam(query.Get("limit"), 50), intParam(query.Get("offset"), 0))
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"departures": departures, "total": total})
}
