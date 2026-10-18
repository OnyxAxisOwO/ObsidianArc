package admin

import (
	"net/http"
	"sort"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func validateMembershipExpiry(at int64) error {
	if at < 0 || (at > 0 && at <= time.Now().UnixMilli()) || at > 253402300799999 {
		return httpx.BadRequest("Membership expiry must be a future date, or zero for permanent membership.")
	}
	return nil
}

func (h *Handlers) assignGroupMembers(w http.ResponseWriter, r *http.Request) error {
	groupID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		UserIDs   []string `json:"user_ids"`
		ExpiresAt int64    `json:"expires_at"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}
	if len(body.UserIDs) == 0 || len(body.UserIDs) > 200 {
		return httpx.BadRequest("Select between 1 and 200 accounts.")
	}
	if err := validateMembershipExpiry(body.ExpiresAt); err != nil {
		return err
	}
	seen := make(map[string]bool, len(body.UserIDs))
	for _, userID := range body.UserIDs {
		if !id.Valid(userID) || seen[userID] {
			return httpx.BadRequest("Account identifiers must be valid and distinct.")
		}
		seen[userID] = true
	}
	// Two operators selecting overlapping batches must take row locks in the
	// same order. The whole batch commits together or leaves everyone alone.
	sort.Strings(body.UserIDs)
	actor := auth.MustUser(r.Context())
	err = h.db.Tx(r.Context(), func(tx *database.Tx) error {
		// The single-account edit refuses to move an administrator the actor
		// cannot manage; a batch is the same write and holds the same line, or
		// a groups grant would reach a super admin's membership through here.
		if err := lockAdminPopulation(r.Context(), tx); err != nil {
			return err
		}
		if _, err := h.groups.ByID(r.Context(), tx, groupID); err != nil {
			return translateGroupError(err)
		}
		current, err := h.users.ByID(r.Context(), tx, actor.ID)
		if err != nil {
			return translateUserError(err)
		}
		for _, userID := range body.UserIDs {
			if _, err := tx.Exec(r.Context(), `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID); err != nil {
				return httpx.Internal(err)
			}
			target, err := h.users.ByID(r.Context(), tx, userID)
			if err != nil {
				return translateUserError(err)
			}
			if target.IsAdmin() && !current.CanManageAdmin(target) {
				return permissionDenied()
			}
			if _, err := h.users.UpdateAdminFields(r.Context(), tx, userID, user.AdminUpdate{
				GroupID: &groupID, GroupExpiresAt: &body.ExpiresAt,
			}); err != nil {
				return translateUserError(err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Each account now has a different group's limits, which the cached ranking
	// reads; see forgetAllowanceRankings.
	h.forgetAllowanceRankings()
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"updated": len(body.UserIDs)})
}
