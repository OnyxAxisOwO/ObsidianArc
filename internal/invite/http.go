package invite

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers is an account's own view of invite codes: its personal code, who
// has used it, and the reward it has earned. Nothing here is administrative
// — every route sits behind auth.RequireUser alone, the same as notify and
// card's account-facing routes — and the console dispatches into these same
// handlers, so they need no browser session either.
type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/profile/invites", auth.RequireUser(httpx.Wrap(h.profile)))
	mux.Handle("POST /api/profile/invites/regenerate", auth.RequireUser(httpx.Wrap(h.regenerate)))
	mux.Handle("POST /api/profile/invites/claim", auth.RequireUser(httpx.Wrap(h.claim)))
}

// payload builds the one shape both GET and regenerate answer with: GET
// reads it as it stands, and regenerate answers with it fresh once the new
// code exists, so a client never has to follow one response with another
// just to redraw the panel.
//
// Built even when personal invites are switched off: the claim box above
// this section works from settings.InvitesRewardCards/Every alone and has
// nothing to do with whether this account has a personal code of its own, so
// reward_every, reward_cards and reward_card_days are always present.
func (h *Handlers) payload(ctx context.Context, accountID string) (map[string]any, error) {
	enabled, limit, rewardCards, rewardCardDays, rewardEvery := h.store.Settings()
	out := map[string]any{
		"enabled": enabled, "code": "", "limit": limit, "used": 0, "counted": 0,
		"reward_cards": rewardCards, "reward_card_days": rewardCardDays, "reward_every": rewardEvery,
		"next_reward_in": 0,
		"invitees":       []any{},
	}
	if !enabled {
		return out, nil
	}

	code, err := h.store.PersonalCode(ctx, accountID)
	if err != nil {
		return nil, err
	}
	uses, err := h.store.UsesByInviter(ctx, accountID)
	if err != nil {
		return nil, err
	}
	counted, err := countedInvites(ctx, h.store.db, accountID)
	if err != nil {
		return nil, err
	}
	var totalUses int
	if err := h.store.db.QueryRow(ctx, `SELECT COUNT(*) FROM invite_uses WHERE inviter_id = ?`, accountID).Scan(&totalUses); err != nil {
		return nil, err
	}

	out["code"] = code.Code
	out["used"] = totalUses
	out["counted"] = counted
	out["next_reward_in"] = nextRewardIn(counted, rewardEvery, rewardCards)
	invitees := make([]map[string]any, 0, len(uses))
	for _, use := range uses {
		invitees = append(invitees, map[string]any{
			"nickname": use.Nickname, "username": use.Username, "created_at": use.CreatedAt,
			"counted":        use.RewardedAt != 0 && use.RewardSkipped == "",
			"reward_cards":   use.RewardCards,
			"reward_skipped": use.RewardSkipped,
		})
	}
	out["invitees"] = invitees
	return out, nil
}

func (h *Handlers) profile(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	out, err := h.payload(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handlers) regenerate(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if enabled, _, _, _, _ := h.store.Settings(); !enabled {
		return httpx.ForbiddenCode("invites_disabled", "Personal invite codes are switched off on this server.")
	}
	if _, err := h.store.Regenerate(r.Context(), account.ID); err != nil {
		var throttled *RegenerateThrottled
		if errors.As(err, &throttled) {
			retryAfter := int(throttled.RetryAfter.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			return httpx.TooManyRequests("too_many_attempts", throttled.Error()).
				WithDetails(map[string]any{"retry_after_seconds": retryAfter})
		}
		return httpx.Internal(err)
	}
	out, err := h.payload(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type claimRequest struct {
	Code string `json:"code"`
}

// claim redeems a batch or partner code for the signed-in account itself —
// see Store.Claim for the rules. Reachable from /register?invite=CODE
// redirecting a signed-in visitor to the settings screen with the code
// pre-filled, and from the console, since it is mounted the same way the
// other profile invite routes are.
func (h *Handlers) claim(w http.ResponseWriter, r *http.Request) error {
	var body claimRequest
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	account := auth.MustUser(r.Context())
	result, err := h.store.Claim(r.Context(), account.ID, body.Code)
	if err != nil {
		var throttled *ClaimThrottled
		if errors.As(err, &throttled) {
			retryAfter := int(throttled.RetryAfter.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			return httpx.TooManyRequests("too_many_attempts", throttled.Error()).
				WithDetails(map[string]any{"retry_after_seconds": retryAfter})
		}
		switch {
		case errors.Is(err, ErrClaimed):
			return httpx.Conflict("invite_claimed", "You have already claimed this code.")
		case errors.Is(err, ErrGroupConflict):
			return httpx.Conflict("invite_group_conflict",
				"Claiming this code would change a group membership you already have.")
		case errors.Is(err, ErrInvalid):
			return httpx.BadRequestCode("invite_invalid", "That invite code is not valid.")
		default:
			return httpx.Internal(err)
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, result)
}
