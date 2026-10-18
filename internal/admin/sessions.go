package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// An account's own signed-in devices, from the operator's side: the same
// shape internal/auth/http.go serves an account about itself, minus the
// "current" flag — nothing about an administrator's own browser is relevant
// to somebody else's session list.
//
// Authorisation follows resetPassword's rule in people.go rather than the
// plain "users" grant every other route on this file checks: a delegated
// administrator holding "users" must not be able to sign an administrator
// they may not manage out of every device that administrator owns, which is
// exactly the same escalation resetPassword already guards against for a
// credential.

type adminSessionPayload struct {
	ID         string `json:"id"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
}

// authoriseSessionAction loads the target and applies resetPassword's rule
// before any read or write against its sessions.
func (h *Handlers) authoriseSessionAction(r *http.Request, userID string) error {
	actor := auth.MustUser(r.Context())
	target, err := h.users.ByID(r.Context(), nil, userID)
	if err != nil {
		return translateUserError(err)
	}
	if !actor.CanAdmin("users") || (target.IsAdmin() && !actor.CanManageAdmin(target)) {
		return permissionDenied()
	}
	return nil
}

func (h *Handlers) userSessions(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.authoriseSessionAction(r, userID); err != nil {
		return err
	}

	sessions, err := h.auth.Sessions().ListByUser(r.Context(), userID)
	if err != nil {
		return httpx.Internal(err)
	}
	out := make([]adminSessionPayload, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, adminSessionPayload{
			ID: auth.SessionRef(s.ID), CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, IP: s.IP, UserAgent: s.UserAgent,
		})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (h *Handlers) revokeUserSession(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.authoriseSessionAction(r, userID); err != nil {
		return err
	}

	ref := r.PathValue("sid")
	if !auth.ValidSessionRef(ref) {
		return httpx.BadRequestCode("malformed_session_id", "Malformed session id.")
	}
	removed, err := h.auth.Sessions().DeleteByPrefixForUser(r.Context(), userID, ref)
	if err != nil {
		return httpx.Internal(err)
	}
	if !removed {
		return httpx.NotFoundCode("session_not_found", "No such session.")
	}
	return httpx.NoContent(w)
}

// revokeAllUserSessions is "sign out everywhere" — every session on the
// account, unlike revokeUserSession's single row. There is no session of the
// operator's own on the line here the way there is on the account's own
// screen, so nothing is excluded.
func (h *Handlers) revokeAllUserSessions(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.authoriseSessionAction(r, userID); err != nil {
		return err
	}
	if err := h.auth.Sessions().DeleteByUser(r.Context(), nil, userID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}
