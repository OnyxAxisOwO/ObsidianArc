package admin

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
)

// Minting, listing, revoking and reading back invite codes. The account-
// facing half of the same feature — a personal code and its own use list —
// is served by internal/invite/http.go under /api/profile/invites, not here:
// this file is only what makes a code exist for somebody else to use.

func translateInviteError(err error) error {
	switch {
	case errors.Is(err, invite.ErrNotFound):
		return httpx.NotFound("No such invite code.")
	case errors.Is(err, invite.ErrCodeTaken):
		return httpx.Conflict("invite_code_taken", "That code already exists.")
	case errors.Is(err, invite.ErrCodeFormat):
		return httpx.BadRequestCode("invite_code_format",
			"A custom code must be 4-32 characters of A-Z and 0-9.")
	case errors.Is(err, invite.ErrNamedBatch):
		return httpx.BadRequest("A batch of more than one code is generated; it cannot be given a code of its own.")
	case errors.Is(err, invite.ErrInvalidCount):
		return httpx.BadRequest("Count must be between 1 and %d.", invite.MaxBatch)
	case errors.Is(err, invite.ErrInvalidMaxUses):
		return httpx.BadRequest("Max uses must be between 0 and %d.", invite.MaxMaxUses)
	case errors.Is(err, invite.ErrInvalidGroupDays):
		return httpx.BadRequest("Group days must be between 0 and %d, group_days_max must be 0 or at least group_days, and a range must start at 1 or more.", invite.MaxGroupDays)
	case errors.Is(err, invite.ErrGroupRequired):
		return httpx.BadRequest("group_days_max requires a group to be set.")
	case errors.Is(err, invite.ErrPartnerFields):
		return httpx.BadRequestCode("invite_partner_fields",
			"A partner code needs a count of 1, a custom code, a name (1-%d characters) and a group.",
			invite.MaxPartnerNameChars)
	default:
		return httpx.Internal(err)
	}
}

func (h *Handlers) listInvites(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	codes, total, err := h.invites.List(r.Context(), invite.ListFilter{
		Kind:   query.Get("kind"),
		Status: query.Get("status"),
		Query:  query.Get("q"),
		Limit:  intParam(query.Get("limit"), 50),
		Offset: intParam(query.Get("offset"), 0),
	})
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"codes": codes, "total": total})
}

type createInvitesRequest struct {
	Count int    `json:"count"`
	Code  string `json:"code"`
	// "batch" (the default, omitted) or "partner" — see invite.CreateInput.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// nil when the field is left out of the request entirely, so Create can
	// tell "not mentioned" from "explicitly turned off" and fall back to the
	// kind's own default (on for a partner) only in the former case.
	AllowExisting *bool  `json:"allow_existing"`
	MaxUses       int    `json:"max_uses"`
	ExpiresAt     int64  `json:"expires_at"`
	GroupID       string `json:"group_id"`
	GroupDays     int    `json:"group_days"`
	GroupDaysMax  int    `json:"group_days_max"`
	Note          string `json:"note"`
}

func (h *Handlers) createInvites(w http.ResponseWriter, r *http.Request) error {
	var body createInvitesRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}
	if body.GroupID != "" {
		if !isValidID(body.GroupID) {
			return httpx.BadRequest("Malformed group id.")
		}
		if _, err := h.groups.ByID(r.Context(), nil, body.GroupID); err != nil {
			return translateGroupError(err)
		}
	}

	actor := auth.MustUser(r.Context())
	codes, err := h.invites.Create(r.Context(), invite.CreateInput{
		Count: body.Count, Code: body.Code, Kind: body.Kind, Name: body.Name, AllowExisting: body.AllowExisting,
		MaxUses: body.MaxUses, ExpiresAt: body.ExpiresAt,
		GroupID: body.GroupID, GroupDays: body.GroupDays, GroupDaysMax: body.GroupDaysMax,
		Note: body.Note, CreatedBy: actor.ID,
	})
	if err != nil {
		return translateInviteError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"codes": codes})
}

func (h *Handlers) revokeInvite(w http.ResponseWriter, r *http.Request) error {
	codeID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	code, err := h.invites.Revoke(r.Context(), codeID)
	if err != nil {
		return translateInviteError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": code})
}

func (h *Handlers) inviteUses(w http.ResponseWriter, r *http.Request) error {
	codeID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := h.invites.ByID(r.Context(), codeID); err != nil {
		return translateInviteError(err)
	}
	uses, err := h.invites.Uses(r.Context(), codeID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"uses": uses})
}

func (h *Handlers) inviteStats(w http.ResponseWriter, r *http.Request) error {
	stats, err := h.invites.Stats(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, stats)
}
