package card

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Handlers is what an account does with its own cards. Creating them, and
// creating the codes that mint them, is administrative and lives in
// internal/admin.
type Handlers struct {
	store *Store
	// Takes the allowance lock before the card row is touched. A reservation
	// that may spend a card takes that lock first and the card row after it, so
	// a spend that took the card first could wait on a reservation that already
	// holds the lock while that reservation waits on the card. Wired in
	// server.go, because the lock belongs to internal/quota.
	LockReset func(context.Context, database.Queryer) error
	// Performs the reset a spent card pays for, inside the transaction that
	// spends the card. Wired in server.go, because the counters belong to
	// internal/quota and this package has no business knowing they exist.
	OnSpend func(context.Context, database.Queryer, user.User, Card) error
	// Resolves who is calling, for the guessing limit below. Optional; nil
	// keys the limit on the account alone.
	ClientIP func(*http.Request) string
	// Checked before a code is looked up, so an automated caller cannot use
	// the endpoint as an oracle when the operator has asked for proof first.
	Challenge turnstile.Gate
	// Its own limiter rather than the sign-in one. The buckets are keyed by
	// address as well as by account, so sharing the map would let somebody
	// who mistyped six codes lock themselves out of logging in.
	guesses *auth.Limiter
}

func NewHandlers(store *Store) *Handlers {
	return &Handlers{store: store, guesses: auth.NewLimiter()}
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}
	mux.Handle("GET /api/usage/cards", protected(h.mine))
	mux.Handle("POST /api/usage/cards/{id}/use", protected(h.use))
	mux.Handle("POST /api/usage/redeem", protected(h.redeem))
}

func (h *Handlers) mine(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	cards, err := h.store.Available(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"cards": cards})
}

// use spends a card and performs the reset it pays for, in one transaction.
//
// The mark and the reset commit together or not at all. A card marked used
// with no reset, or a reset with the card still unspent, would each be a way
// to lose a card or to reset an account again on the next press.
//
// The order inside is the reservation path's: the allowance lock, then the card
// row, then the counters. The transaction runs on a context that outlives the
// request, so a browser that disconnects part way cannot decide the outcome
// after the fact; the server finishes what it started.
func (h *Handlers) use(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	cardID := r.PathValue("id")
	if cardID == "" {
		return httpx.BadRequest("A card is required.")
	}

	// Refused before the card is touched: a handler with no reset wired would
	// otherwise spend the card and answer 204 with nothing restored.
	if h.LockReset == nil || h.OnSpend == nil {
		return httpx.Internal(errors.New("card: no reset hook is wired"))
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancel()
	err := h.store.db.Tx(ctx, func(tx *database.Tx) error {
		if err := h.LockReset(ctx, tx); err != nil {
			return err
		}
		card, err := h.store.SpendCard(ctx, tx, account.ID, cardID)
		if err != nil {
			return err
		}
		return h.OnSpend(ctx, tx, account, card)
	})
	if err != nil {
		return translate(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) redeem(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	var body struct {
		Code      string `json:"code"`
		Turnstile string `json:"turnstile"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}

	// A code is a secret that is typed, and an administrator may deliberately
	// mint a memorable one, so guessing at them has to get slower the same way
	// guessing at a password does. translate() below already refuses to say
	// whether an unknown code exists; without a limit that only slowed one
	// guess down, not the dictionary behind it.
	ip := ""
	if h.ClientIP != nil {
		ip = h.ClientIP(r)
	}
	// Taken before the outbound verification, not after it: a request the
	// limiter would refuse must not cost a round trip to the challenge service.
	attempt, err := h.guesses.Begin(ip, account.ID)
	if err != nil {
		var limited *auth.RateLimitError
		if errors.As(err, &limited) {
			w.Header().Set("Retry-After", strconv.Itoa(int(limited.RetryAfter.Seconds())+1))
			return httpx.TooManyRequests("too_many_attempts", limited.Error())
		}
		return httpx.Internal(err)
	}
	// Releases the reservation on every path out. A redemption that worked
	// does not clear the count: only time does.
	defer attempt.Cancelled()

	if err := h.Challenge.Check(r.Context(), body.Turnstile, ip); err != nil {
		// A refused challenge is a failed attempt, or a caller who never solves
		// one could make verification calls without limit. An unavailable service
		// is not the caller's failure, so it is not counted.
		if errors.Is(err, turnstile.ErrFailed) {
			attempt.Failed()
		}
		return redeemChallengeError(err)
	}

	record, err := h.store.Redeem(r.Context(), account.ID, body.Code)
	if err != nil {
		// A code that exists and simply is not for this account is a typo, not
		// a guess; only "no such code" says the caller was searching.
		if errors.Is(err, ErrCodeUnknown) || errors.Is(err, ErrCodeExpired) {
			attempt.Failed()
		}
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"card": record})
}

// TranslateError maps this package's sentinels onto responses. Exported so
// the administrative half answers the same way about the same failures.
func TranslateError(err error) error { return translate(err) }

func redeemChallengeError(err error) error {
	switch {
	case errors.Is(err, turnstile.ErrFailed):
		return httpx.ForbiddenCode("challenge_failed",
			"Complete the verification before redeeming a code.")
	case errors.Is(err, turnstile.ErrUnavailable):
		return httpx.UnavailableCode("challenge_unavailable",
			"Verification is unavailable right now. Try again shortly.")
	default:
		return httpx.Internal(err)
	}
}

func translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("That card does not exist.")
	case errors.Is(err, ErrUsed):
		return httpx.Conflict("card_used", "That card has already been used.")
	case errors.Is(err, ErrExpired):
		return httpx.Conflict("card_expired", "That card has expired.")
	// An unknown code and an expired one answer the same way on purpose: the
	// difference is only useful to somebody guessing at codes.
	case errors.Is(err, ErrCodeUnknown), errors.Is(err, ErrCodeExpired):
		return httpx.NotFound("That code is not valid.")
	case errors.Is(err, ErrCodeEmpty):
		return httpx.Conflict("code_empty", "That code has been fully redeemed.")
	case errors.Is(err, ErrCodeUsed):
		return httpx.Conflict("code_used", "You have already redeemed that code.")
	case errors.Is(err, ErrCodeTaken):
		return httpx.Conflict("code_exists", "That code already exists.")
	case errors.Is(err, ErrInvalidCode):
		return httpx.BadRequest("A code is required.")
	case errors.Is(err, ErrNamedBatch):
		return httpx.BadRequest("Leave the code empty when generating more than one.")
	case errors.Is(err, ErrBatchTooLarge):
		return httpx.BadRequestCode("card_batch_too_large", "A batch makes at most %d codes at a time.", MaxBatch)
	case errors.Is(err, ErrInvalidCount):
		return httpx.BadRequest("At least one card is required.")
	case errors.Is(err, ErrInvalidExpiry):
		return httpx.BadRequest("Choose a card expiry in the future, within ten years.")
	case errors.Is(err, ErrInvalidWindow):
		return httpx.BadRequest("Valid windows are: 5h, 1w, 1m, or full.")
	}
	return httpx.Internal(err)
}
