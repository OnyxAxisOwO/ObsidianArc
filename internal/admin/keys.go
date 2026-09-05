package admin

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// What an operator can see and do about the keys an account has issued.
//
// Only ever the record, never the credential: the server keeps a digest, so
// there is nothing here that could show a token even to an administrator, and
// no endpoint that could be added later to change that. What an operator gets
// is the list — what exists, what it is called, when it dies, whether it is
// still being used — and the ability to revoke, which is the thing they
// actually need when an account is compromised or leaves.

func (h *Handlers) userKeys(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	// Confirms the account exists, so a guessed id reads as "no such user"
	// rather than as an empty list.
	if _, err := h.users.ByID(r.Context(), nil, userID); err != nil {
		return translateUserError(err)
	}

	keys, err := h.keys.List(r.Context(), userID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *Handlers) revokeUserKey(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	keyID := r.PathValue("key")
	if !id.Valid(keyID) {
		return httpx.NotFound("No such key.")
	}

	// Scoped to the named account rather than deleting by key id alone: an
	// administrator revoking someone's key should not be able to revoke a
	// different person's by mistyping a path.
	if err := h.keys.Delete(r.Context(), userID, keyID); err != nil {
		if errors.Is(err, apikey.ErrNotFound) {
			return httpx.NotFound("No such key.")
		}
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}
