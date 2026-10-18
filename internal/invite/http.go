package invite

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
)

// Handlers is an account's own view of invite codes: its personal code, who
// has used it, and the reward it has earned. Nothing here is administrative
// — every route sits behind auth.RequireUser alone, the same as notify and
// card's account-facing routes — and the console dispatches into these same
// handlers, so they need no browser session either.
type Handlers struct {
	store *Store
	// ClientIP names the address a claim comes from, for the guess budget.
	ClientIP func(*http.Request) string
	// ReserveGuess takes the address's place in the invite-guessing budget that
	// registration draws on (auth.Service.ReserveInviteGuess). Without it a
	// claim is counted only against its account, and an account costs one
	// signup, so the short partner codes could be walked a few guesses at a
	// time from any number of new accounts.
	ReserveGuess func(ip string) (*auth.Attempt, error)
	// Set by a plugin that has something to add to the invitee list: during
	// setup, or when one is installed or removed while the server runs.
	decMu      sync.RWMutex
	decorators []decorator
	gate       plugingate.Gate
}

type decorator struct {
	plugin string
	fn     InviteeDecorator
}

// SetPluginGate is how the server says which plugins are on; a switched-off
// plugin's decorator is skipped.
func (h *Handlers) SetPluginGate(g plugingate.Gate) { h.gate = g }

// Invitee is one row of an inviter's own list, as a decorator sees it. The
// account id is the join key and never reaches the browser; Entry is the
// row as it will be sent.
type Invitee struct {
	UserID string
	Entry  map[string]any
}

// InviteeDecorator may change the rows of inviterID's list, or add rows
// about accounts that no longer have an invite_uses row to be listed from —
// a plugin that deletes accounts keeps its own record of who they were.
type InviteeDecorator func(ctx context.Context, inviterID string, invitees []Invitee) ([]Invitee, error)

// DecorateInvitees adds plugin's fn to the list's decorators, run in the
// order added.
func (h *Handlers) DecorateInvitees(plugin string, fn InviteeDecorator) {
	h.decMu.Lock()
	defer h.decMu.Unlock()
	h.decorators = append(h.decorators, decorator{plugin: plugin, fn: fn})
}

// RemoveDecorators takes plugin's decorators away, for a plugin being
// removed while the server runs.
func (h *Handlers) RemoveDecorators(plugin string) {
	h.decMu.Lock()
	defer h.decMu.Unlock()
	kept := make([]decorator, 0, len(h.decorators))
	for _, d := range h.decorators {
		if d.plugin != plugin {
			kept = append(kept, d)
		}
	}
	h.decorators = kept
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
	policy := h.store.Settings()
	out := map[string]any{
		"enabled": policy.UserEnabled, "code": "", "limit": policy.Limit, "used": 0, "counted": 0,
		"reward_cards": policy.RewardCards, "reward_card_days": policy.RewardCardDays, "reward_every": policy.RewardEvery,
		"next_reward_in": 0,
		"invitees":       []any{},
	}
	if !policy.UserEnabled {
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
	out["next_reward_in"] = nextRewardIn(counted, policy.RewardEvery, policy.RewardCards)

	invitees := make([]Invitee, 0, len(uses))
	for _, use := range uses {
		invitees = append(invitees, Invitee{UserID: use.UserID, Entry: map[string]any{
			"nickname": use.Nickname, "username": use.Username, "created_at": use.CreatedAt,
			"counted":        use.RewardedAt != 0 && use.RewardSkipped == "",
			"reward_cards":   use.RewardCards,
			"reward_skipped": use.RewardSkipped,
		}})
	}
	h.decMu.RLock()
	decorators := append([]decorator(nil), h.decorators...)
	h.decMu.RUnlock()
	for _, decorate := range decorators {
		if !h.gate.Allows(decorate.plugin) {
			continue
		}
		decorated, err := decorate.fn(ctx, accountID, invitees)
		if err != nil {
			// A decorator that fails is skipped and the list keeps the rows it
			// had before that decorator ran, so one plugin's fault does not
			// fail the panel for every inviter, and regenerate does not answer
			// 500 after its new code was committed.
			slog.ErrorContext(ctx, "invite: invitee decorator failed",
				"plugin", decorate.plugin, "account_id", accountID, "error", err)
			continue
		}
		invitees = decorated
	}
	entries := make([]map[string]any, 0, len(invitees))
	for _, invitee := range invitees {
		entries = append(entries, invitee.Entry)
	}
	out["invitees"] = entries
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
	if !h.store.Settings().UserEnabled {
		return httpx.ForbiddenCode("invites_disabled", "Personal invite codes are switched off on this server.")
	}
	if _, err := h.store.Regenerate(r.Context(), account.ID); err != nil {
		var throttled *RegenerateThrottled
		if errors.As(err, &throttled) {
			return tooManyClaims(w, throttled.RetryAfter, throttled.Error())
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

func (h *Handlers) clientIP(r *http.Request) string {
	if h.ClientIP == nil {
		return ""
	}
	return h.ClientIP(r)
}

// tooManyClaims answers a refused claim or regeneration with how long to wait. The retry hint
// is in whole seconds, rounded up, so a client that waits it out is not told
// to wait again immediately.
func tooManyClaims(w http.ResponseWriter, wait time.Duration, message string) error {
	retryAfter := int(wait.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	return httpx.TooManyRequests("too_many_attempts", message).
		WithDetails(map[string]any{"retry_after_seconds": retryAfter})
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

	// A claim whose code is wrong is a guess, so the address's place in the
	// guess budget is held until the claim is decided, as registration holds
	// it. A refusal about the account itself, such as an already-claimed code,
	// is not a guess and releases the place unused.
	var guess *auth.Attempt
	if h.ReserveGuess != nil {
		held, err := h.ReserveGuess(h.clientIP(r))
		if err != nil {
			var limited *auth.RateLimitError
			if errors.As(err, &limited) {
				return tooManyClaims(w, limited.RetryAfter, limited.Error())
			}
			return httpx.Internal(err)
		}
		guess = held
		defer guess.Cancelled()
	}

	result, err := h.store.Claim(r.Context(), account.ID, body.Code)
	if errors.Is(err, ErrInvalid) {
		guess.Failed()
	}
	if err != nil {
		var throttled *ClaimThrottled
		if errors.As(err, &throttled) {
			return tooManyClaims(w, throttled.RetryAfter, throttled.Error())
		}
		switch {
		case errors.Is(err, ErrClaimed):
			return httpx.Conflict("invite_claimed", "You have already claimed this code.")
		case errors.Is(err, ErrGroupConflict):
			return httpx.Conflict("invite_group_conflict",
				"Claiming this code would change a group membership you already have.")
		case errors.Is(err, ErrOwnCode):
			return httpx.ForbiddenCode("invite_own_code",
				"You created this code, so it cannot be claimed on your own account.")
		case errors.Is(err, ErrInvalid):
			return httpx.BadRequestCode("invite_invalid", "That invite code is not valid.")
		default:
			return httpx.Internal(err)
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, result)
}
