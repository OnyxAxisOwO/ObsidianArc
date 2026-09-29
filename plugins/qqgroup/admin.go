// The backoffice's departure actions: processing a member leaving the
// community's group chat, and reading back the ones already processed.
// The heavy lifting is Departures.Depart; what lives here is the guards
// the operator's own surface needs — you cannot process yourself, and the
// response says how much of the inviter's reward was actually clawed back,
// because "due" and "taken" are different numbers an operator reads.
package qqgroup

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type adminHandlers struct {
	departures *Departures
	clientIP   func(*http.Request) string
}

// mount adds both routes to the backoffice's table, behind its own wrapper:
// processing a departure is the users grant's, reading them back is the
// invites grant's, as they were when this was part of the core.
func (h *adminHandlers) mount(backoffice *admin.Handlers) {
	backoffice.Mount(admin.Route{
		Pattern: "POST /api/admin/users/{id}/departure", Permission: "users", Handler: h.departUser, Plugin: Name,
	})
	backoffice.Mount(admin.Route{
		Pattern: "GET /api/admin/departures", Permission: "invites", Handler: h.listDepartures, Plugin: Name,
	})
}

type departBody struct {
	Mode string `json:"mode"`
	Note string `json:"note"`
}

func (h *adminHandlers) departUser(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID := r.PathValue("id")
	if !id.Valid(userID) {
		return httpx.BadRequest("Malformed identifier.")
	}
	if userID == actor.ID {
		return httpx.BadRequest("You cannot process your own departure.")
	}

	var body departBody
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	mode := body.Mode
	if !validMode(mode) {
		return httpx.BadRequestCode("departure_mode", "Mode must be %q or %q.", ModeDisable, ModeDelete)
	}

	ip := ""
	if h.clientIP != nil {
		ip = h.clientIP(r)
	}
	result, err := h.departures.Depart(r.Context(), DepartInput{
		UserID:    userID,
		Mode:      mode,
		Source:    SourceAdmin,
		ActorID:   actor.ID,
		ActorName: actor.Username,
		Note:      body.Note,
		IP:        ip,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrDeparted):
			return httpx.Conflict("already_departed",
				"This account has already been processed for a departure.")
		case errors.Is(err, ErrDepartAdmin):
			return httpx.Conflict("departure_refused",
				"An administrator account can only be processed by an administrator, and never by the bot.")
		case errors.Is(err, ErrDepartLastAdmin):
			return httpx.Conflict("last_admin", "This is the last super administrator; promote someone else first.")
		case errors.Is(err, user.ErrNotFound):
			return httpx.NotFound("No such account.")
		default:
			return httpx.Internal(err)
		}
	}

	slog.WarnContext(r.Context(), "administrator processed a group departure",
		"actor", actor.ID, "target", userID, "mode", mode,
		"cards_due", result.RewardCardsDue, "cards_revoked", result.CardsRevoked)

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"departure": result})
}

func (h *adminHandlers) listDepartures(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	departures, total, err := h.departures.ListDepartures(r.Context(),
		intParam(query.Get("limit"), 50), intParam(query.Get("offset"), 0))
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"departures": departures, "total": total})
}

func intParam(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
