package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Redemption codes, and handing a usage reset card to one account.
//
// The reading half — what an account holds, and spending one — is in
// internal/card, served to the account that owns it. This file is only the
// part that makes them exist.

type codeRequest struct {
	Code      string `json:"code"`
	Cards     int    `json:"cards"`
	CardDays  int    `json:"card_days"`
	ExpiresAt int64  `json:"expires_at"`
	Note      string `json:"note"`
	// How many distinct codes to mint. More than one means they are
	// generated, because a batch cannot all be called the same thing.
	Count int `json:"count"`
}

func (h *Handlers) listCodes(w http.ResponseWriter, r *http.Request) error {
	codes, err := h.cards.ListCodes(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

func (h *Handlers) createCode(w http.ResponseWriter, r *http.Request) error {
	var body codeRequest
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	codes, err := h.cards.CreateCodes(r.Context(), card.CodeInput{
		Code:      body.Code,
		Cards:     body.Cards,
		CardDays:  body.CardDays,
		ExpiresAt: body.ExpiresAt,
		Note:      body.Note,
	}, body.Count)
	if err != nil {
		return card.TranslateError(err)
	}
	// The whole batch, because generated codes exist nowhere else until
	// somebody copies them off this response.
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"codes": codes})
}

func (h *Handlers) deleteCode(w http.ResponseWriter, r *http.Request) error {
	codeID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	// The cards already minted from it are left alone. Somebody redeemed
	// them; withdrawing the code is a decision about who may still redeem,
	// not about taking back what was handed out.
	if err := h.cards.DeleteCode(r.Context(), codeID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) grantCards(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := h.users.ByID(r.Context(), nil, userID); err != nil {
		return translateUserError(err)
	}

	var body struct {
		Cards    int `json:"cards"`
		CardDays int `json:"card_days"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	if body.Cards == 0 {
		body.Cards = 1
	}

	granted, err := h.cards.Grant(r.Context(), userID, body.Cards, body.CardDays)
	if err != nil {
		return card.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"cards": granted})
}
