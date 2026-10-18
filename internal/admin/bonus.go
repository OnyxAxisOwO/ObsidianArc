package admin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/checkin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
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
	// A shared pool's rules. amount is read on create only: what is in a pool
	// afterwards is changed by the top-up, not by saving its rules.
	Pool *bonus.Pool `json:"pool"`
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
	if b.Pool != nil {
		pool := *b.Pool
		pool.Used, pool.PeriodStart = 0, 0
		bar.Pool = &pool
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
	// The instance lock comes before the bar's row. A check-in save holds only
	// the instance lock while it checks the bar it names, so a switch-off that
	// took just the row could land between that check and the save's commit.
	// Taking the lock first also keeps the two from each holding one and waiting
	// on the other: deleteBonusBar takes them in this order too.
	//
	// The row is locked before it is read. Two saves that each read the bar
	// and then write the whole row would otherwise let the later write put back
	// the switch the earlier one turned off.
	var updated bonus.Bar
	err := h.db.Tx(r.Context(), func(tx *database.Tx) error {
		if err := settings.Lock(r.Context(), tx); err != nil {
			return httpx.Internal(err)
		}
		if _, err := tx.Exec(r.Context(), `UPDATE bonus_bars SET updated_at = updated_at WHERE id = ?`, bar.ID); err != nil {
			return httpx.Internal(err)
		}
		// The write replaces the whole bar, so a switch the body leaves out must
		// keep what the bar has. body.bar() fills omitted switches with their
		// create-time defaults, and that would reactivate a bar an operator
		// switched off, or reset its default and total display.
		stored, err := h.Bonus.Bar(r.Context(), tx, bar.ID)
		if err != nil {
			return bonus.TranslateError(err)
		}
		if body.DefaultOn == nil {
			bar.DefaultOn = stored.DefaultOn
		}
		if body.ShowTotal == nil {
			bar.ShowTotal = stored.ShowTotal
		}
		if body.Active == nil {
			bar.Active = stored.Active
		}
		if body.Pool == nil {
			bar.Pool = stored.Pool
		}
		// Only the switch-off is refused. A bar that is already off may still be
		// renamed or re-priced, even while a check-in names it.
		if stored.Active && !bar.Active {
			if err := h.checkinPaysFrom(r.Context(), tx, bar.ID); err != nil {
				return err
			}
		}
		saved, err := h.Bonus.UpdateBar(r.Context(), tx, bar)
		if err != nil {
			return bonus.TranslateError(err)
		}
		updated = saved
		return nil
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"bar": updated})
}

// adjustBonusPool tops a shared pool up, takes from it, or sets what is left.
func (h *Handlers) adjustBonusPool(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		// "add" (the default) or "set".
		Op     string   `json:"op"`
		Amount *float64 `json:"amount"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	if body.Amount == nil || (body.Op != "" && body.Op != "add" && body.Op != "set") {
		return httpx.BadRequest("Say how much, and whether to add it or set what is left to it.")
	}
	pool, err := h.Bonus.AdjustPool(r.Context(), r.PathValue("id"), body.Op == "set", *body.Amount)
	if err != nil {
		return bonus.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"pool": pool})
}

func (h *Handlers) deleteBonusBar(w http.ResponseWriter, r *http.Request) error {
	barID := r.PathValue("id")
	// The check and the delete share one transaction under the instance lock,
	// so a check-in save that names this bar either commits before the check,
	// and the refusal sees it, or waits until the bar is gone and is refused by
	// its own check.
	err := h.db.Tx(r.Context(), func(tx *database.Tx) error {
		if err := settings.Lock(r.Context(), tx); err != nil {
			return httpx.Internal(err)
		}
		if err := h.checkinPaysFrom(r.Context(), tx, barID); err != nil {
			return err
		}
		if err := h.Bonus.DeleteBar(r.Context(), tx, barID); err != nil {
			return bonus.TranslateError(err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// checkinPaysFrom refuses to remove or switch off a bar that a check-in reward
// still names. The rewards are saved against the bar's id, so the break would
// show only when an account reached that reward, as a claim that cannot be
// paid, long after whoever removed the bar has forgotten the link.
//
// It reads the saved rewards through q rather than the settings cache: the
// cache is filled only after a save commits, so a save that committed just
// before the instance lock was taken would be missed.
func (h *Handlers) checkinPaysFrom(ctx context.Context, q database.Queryer, barID string) error {
	if h.Checkin == nil {
		return nil
	}
	cfg, err := h.Checkin.StoredConfig(ctx, q)
	if err != nil {
		return httpx.Internal(err)
	}
	if cfg.Daily.Kind == checkin.KindBonus && cfg.Daily.BarID == barID {
		return httpx.Conflict("bonus_bar_in_checkin", "The daily check-in reward pays from this bonus. Change the check-in rewards first.")
	}
	for _, rule := range cfg.Rules {
		if rule.Reward.Kind == checkin.KindBonus && rule.Reward.BarID == barID {
			return httpx.Conflict("bonus_bar_in_checkin",
				fmt.Sprintf("The check-in milestone %q pays from this bonus. Change the check-in rewards first.", rule.Title))
		}
	}
	return nil
}

// grantBonus gives an amount in a bar to everyone, a group or named accounts.
func (h *Handlers) grantBonus(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		All     bool     `json:"all"`
		GroupID string   `json:"group_id"`
		UserIDs []string `json:"user_ids"`
		// Or by name, which is what a person has to hand.
		Usernames []string `json:"usernames"`
		Amount    float64  `json:"amount"`
		// One of the two: an explicit moment, or a number of days from now.
		ExpiresAt int64  `json:"expires_at"`
		Days      int    `json:"days"`
		Note      string `json:"note"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}
	// Neither branch below takes a negative count, so it would leave the expiry at
	// zero, which the store keeps as a credit that never expires.
	if body.Days < 0 {
		return httpx.BadRequest("Days cannot be negative.")
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
	for _, set := range []bool{body.All, body.GroupID != "", len(body.UserIDs)+len(body.Usernames) > 0} {
		if set {
			targets++
		}
	}
	if targets != 1 {
		return httpx.BadRequest("Say who it goes to: everyone, one group, or named accounts.")
	}
	actor := auth.MustUser(r.Context())
	result, err := h.Bonus.Grant(r.Context(), barID,
		bonus.Target{All: body.All, GroupID: body.GroupID, UserIDs: body.UserIDs, Usernames: body.Usernames},
		body.Amount, expires, "admin", body.Note, actor.ID)
	if err != nil {
		return bonus.TranslateError(err)
	}

	params := map[string]any{"name": bar.Name, "amount": body.Amount}
	switch {
	case body.All:
		h.push(r.Context(), notify.Notification{Audience: notify.AudienceAll, Kind: "bonus_granted", Params: params, Link: "/usage"})
	default:
		// Exactly the accounts the grant was written to, from the same
		// transaction. Asking again here would notify a different set when
		// membership changed in between, and a name given twice was told twice.
		for _, userID := range result.UserIDs {
			h.tellAccount(r.Context(), actor, userID, "bonus_granted", "/usage", params)
		}
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"granted": result.Count, "amount": body.Amount, "expires_at": expires,
	})
}

func (h *Handlers) bonusGrants(w http.ResponseWriter, r *http.Request) error {
	limit := intParam(r.URL.Query().Get("limit"), 0)
	offset := intParam(r.URL.Query().Get("offset"), 0)
	rows, total, err := h.Bonus.GrantsInBar(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"grants": rows, "total": total})
}

func (h *Handlers) revokeBonusGrant(w http.ResponseWriter, r *http.Request) error {
	grantID := r.PathValue("id")
	// The usage grant takes money away from the grant's owner, so the owner
	// decides whether it may: the rule every other delegated action on an
	// account answers to, not the grant alone.
	var owner string
	if err := h.db.QueryRow(r.Context(), `SELECT user_id FROM bonus_grants WHERE id = ?`, grantID).Scan(&owner); err != nil {
		if database.IsNotFound(err) {
			return bonus.TranslateError(bonus.ErrNotFound)
		}
		return httpx.Internal(err)
	}
	target, err := h.users.ByID(r.Context(), nil, owner)
	if err != nil {
		return translateUserError(err)
	}
	if target.IsAdmin() && !auth.MustUser(r.Context()).CanManageAdmin(target) {
		return permissionDenied()
	}
	revoked, err := h.Bonus.Revoke(r.Context(), grantID)
	if err != nil {
		return bonus.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}
