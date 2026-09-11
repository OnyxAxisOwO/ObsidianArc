package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Trying the sign-up reviewer without registering anything.
//
// It exists because the question "is the review not working, or is it working
// and being generous" has no other answer from outside: a registration that
// went through looks the same either way. Here an operator types the account
// that got past it and reads what the model actually said.
//
// The reviewer's own reasoning is shown here and nowhere else. This is the
// operator's screen; what a refused visitor sees is still only the message
// the operator wrote.

type reviewTrialRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	QQ        string `json:"qq"`
	Nickname  string `json:"nickname"`
	UserAgent string `json:"user_agent"`
	// What the real check would have counted. Typed rather than measured,
	// because the point is to try a case, not to reproduce one.
	FromThisAddress int `json:"from_this_address"`
}

func (h *Handlers) trialReview(w http.ResponseWriter, r *http.Request) error {
	if h.TryReview == nil {
		return httpx.BadRequest("This build has no sign-up reviewer.")
	}

	var body reviewTrialRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}
	if body.Username == "" {
		return httpx.BadRequest("A username is required to review.")
	}

	decision, reason, err := h.TryReview(r.Context(), ReviewTrial{
		Username: body.Username, Email: body.Email, QQ: body.QQ,
		Nickname: body.Nickname, UserAgent: body.UserAgent,
		FromThisAddress: body.FromThisAddress,
	})
	if err != nil {
		// Reported rather than returned as a failure: an unreachable model is
		// exactly the answer somebody is here to find. The decision beside it
		// is the configured mode's fallback, not a claim that the model ran.
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"ran": false, "decision": decision, "reason": err.Error(),
		})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ran": true, "decision": decision, "reason": reason,
	})
}

// ReviewTrial is one hypothetical sign-up. Declared here rather than taken
// from internal/screening so this package does not depend on it — the wiring
// owns that, as it does for the real check.
type ReviewTrial struct {
	Username        string
	Email           string
	QQ              string
	Nickname        string
	UserAgent       string
	FromThisAddress int
}
