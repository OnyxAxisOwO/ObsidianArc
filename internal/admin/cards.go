package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Redemption codes, and handing a usage reset card to one account.
//
// The reading half — what an account holds, and spending one — is in
// internal/card, served to the account that owns it. This file is only the
// part that makes them exist.

type codeRequest struct {
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	Windows   []string `json:"windows"`
	Cards     int      `json:"cards"`
	CardDays  int      `json:"card_days"`
	ExpiresAt int64    `json:"expires_at"`
	Note      string   `json:"note"`
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
		Name:      body.Name,
		Windows:   body.Windows,
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

func (h *Handlers) codeRedemptions(w http.ResponseWriter, r *http.Request) error {
	codeID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	redemptions, err := h.cards.CodeRedemptions(r.Context(), codeID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"redemptions": redemptions})
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
		Name      string   `json:"name"`
		Windows   []string `json:"windows"`
		Cards     int      `json:"cards"`
		CardDays  int      `json:"card_days"`
		ExpiresAt int64    `json:"expires_at"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	if body.Cards == 0 {
		body.Cards = 1
	}

	var granted []card.Card
	if body.ExpiresAt > 0 {
		granted, err = h.cards.GrantUntilNamed(r.Context(), userID, body.Cards, body.ExpiresAt, body.Name, body.Windows)
	} else {
		// Kept for clients from before the date picker existed.
		granted, err = h.cards.GrantNamed(r.Context(), nil, userID, body.Cards, body.CardDays, body.Name, body.Windows)
	}
	if err != nil {
		return card.TranslateError(err)
	}
	h.tellAccount(r.Context(), auth.MustUser(r.Context()), userID, "cards_granted", "/usage",
		map[string]any{"count": len(granted)})

	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"cards": granted})
}

// rescheduleCards moves the expiry on cards an account already holds.
//
// Separate from granting rather than a flag on it: "here is another card" and
// "the one you have lasts longer" are different answers to the same request,
// and an operator who meant the second should not be able to produce the
// first by mistyping a field.
func (h *Handlers) rescheduleCards(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := h.users.ByID(r.Context(), nil, userID); err != nil {
		return translateUserError(err)
	}

	var body struct {
		ExpiresAt int64 `json:"expires_at"`
		// Which cards to move. Absent means every unused one this account
		// holds, expired included — the bulk spelling.
		CardIDs []string `json:"card_ids"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}

	moved, err := h.cards.Reschedule(r.Context(), userID, body.CardIDs, body.ExpiresAt)
	if err != nil {
		return card.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"moved": moved})
}

// revokeCard takes one unused card back off an account.
//
// No notice reaches the account: cards arrive without one and the whole
// feature has never had a message attached to it in either direction. An
// operator correcting a mis-typed grant should not be announcing it.
func (h *Handlers) revokeCard(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	cardID, err := pathID(r, "card")
	if err != nil {
		return err
	}
	if _, err := h.users.ByID(r.Context(), nil, userID); err != nil {
		return translateUserError(err)
	}
	if err := h.cards.Revoke(r.Context(), userID, cardID); err != nil {
		return card.TranslateError(err)
	}
	return httpx.NoContent(w)
}
