package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
)

func (h *Handlers) bonusBars(w http.ResponseWriter, r *http.Request) error {
	bars, err := h.Bonus.Bars(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"bars": bars})
}

type bonusBarBody struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Kind             string   `json:"kind"`
	ToggleMode       string   `json:"toggle_mode"`
	DefaultOn        *bool    `json:"default_on"`
	ShowTotal        *bool    `json:"show_total"`
	ModelIDs         []string `json:"model_ids"`
	DefaultExpiresAt int64    `json:"default_expires_at"`
	Active           *bool    `json:"active"`
}

func (b bonusBarBody) bar() bonus.Bar {
	bar := bonus.Bar{
		Name: b.Name, Description: b.Description, Kind: bonus.Kind(b.Kind), ToggleMode: bonus.Mode(b.ToggleMode),
		DefaultOn: true, ShowTotal: true, ModelIDs: b.ModelIDs, DefaultExpiresAt: b.DefaultExpiresAt, Active: true,
	}
	if b.DefaultOn != nil {
		bar.DefaultOn = *b.DefaultOn
	}
	if b.ShowTotal != nil {
		bar.ShowTotal = *b.ShowTotal
	}
	if b.Active != nil {
		bar.Active = *b.Active
	}
	return bar
}

func (h *Handlers) createBonusBar(w http.ResponseWriter, r *http.Request) error {
	var body bonusBarBody
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}
	bar, err := h.Bonus.CreateBar(r.Context(), body.bar(), auth.MustUser(r.Context()).ID)
	if err != nil {
		return bonus.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"bar": bar})
}

func (h *Handlers) updateBonusBar(w http.ResponseWriter, r *http.Request) error {
	var body bonusBarBody
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}
	bar := body.bar()
	bar.ID = r.PathValue("id")
	updated, err := h.Bonus.UpdateBar(r.Context(), bar)
	if err != nil {
		return bonus.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"bar": updated})
}

func (h *Handlers) deleteBonusBar(w http.ResponseWriter, r *http.Request) error {
	if err := h.Bonus.DeleteBar(r.Context(), r.PathValue("id")); err != nil {
		return bonus.TranslateError(err)
	}
	return httpx.NoContent(w)
}

// grantBonus gives an amount in a bar to everyone, a group or named accounts.
func (h *Handlers) grantBonus(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		All     bool     `json:"all"`
		GroupID string   `json:"group_id"`
		UserIDs []string `json:"user_ids"`
		Amount  float64  `json:"amount"`
		// One of the two: an explicit moment, or a number of days from now.
		ExpiresAt int64  `json:"expires_at"`
		Days      int    `json:"days"`
		Note      string `json:"note"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}
	barID := r.PathValue("id")
	bar, err := h.Bonus.Bar(r.Context(), nil, barID)
	if err != nil {
		return bonus.TranslateError(err)
	}
	expires := body.ExpiresAt
	switch {
	case expires == 0 && body.Days > 0:
		expires = time.Now().Add(time.Duration(min(body.Days, 3650)) * 24 * time.Hour).UnixMilli()
	case expires == 0 && body.Days == 0:
		expires = bar.DefaultExpiresAt
	}
	targets := 0
	for _, set := range []bool{body.All, body.GroupID != "", len(body.UserIDs) > 0} {
		if set {
			targets++
		}
	}
	if targets != 1 {
		return httpx.BadRequest("Say who it goes to: everyone, one group, or named accounts.")
	}
	actor := auth.MustUser(r.Context())
	result, err := h.Bonus.Grant(r.Context(), barID,
		bonus.Target{All: body.All, GroupID: body.GroupID, UserIDs: body.UserIDs},
		body.Amount, expires, "admin", body.Note, actor.ID)
	if err != nil {
		return bonus.TranslateError(err)
	}

	params := map[string]any{"name": bar.Name, "amount": body.Amount}
	switch {
	case body.All:
		h.push(r.Context(), notify.Notification{Audience: notify.AudienceAll, Kind: "bonus_granted", Params: params, Link: "/usage"})
	default:
		for _, userID := range h.bonusRecipients(r, body.GroupID, body.UserIDs) {
			h.tellAccount(r.Context(), actor, userID, "bonus_granted", "/usage", params)
		}
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"granted": result.Count, "amount": body.Amount, "expires_at": expires,
	})
}

// bonusRecipients is who to tell about a grant that did not go to everyone.
func (h *Handlers) bonusRecipients(r *http.Request, groupID string, userIDs []string) []string {
	if groupID == "" {
		return userIDs
	}
	rows, err := h.db.Query(r.Context(), `SELECT id FROM users WHERE group_id = ?`, groupID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func (h *Handlers) bonusGrants(w http.ResponseWriter, r *http.Request) error {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, total, err := h.Bonus.GrantsInBar(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"grants": rows, "total": total})
}

func (h *Handlers) revokeBonusGrant(w http.ResponseWriter, r *http.Request) error {
	revoked, err := h.Bonus.Revoke(r.Context(), r.PathValue("id"))
	if err != nil {
		return bonus.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}
