package checkin

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Handlers is what an account does with its own check-ins.
type Handlers struct {
	service *Service
	// Whether an unconfirmed address may not check in, as it may not chat: a
	// reward for pressing a button is what a throwaway account is made for.
	VerificationRequired func() bool
}

func NewHandlers(service *Service) *Handlers { return &Handlers{service: service} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}
	mux.Handle("GET /api/checkin", protected(h.status))
	mux.Handle("POST /api/checkin", protected(h.checkIn))
	mux.Handle("POST /api/checkin/claims/{rule}", protected(h.claim))
}

func (h *Handlers) status(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	st, err := h.service.StatusOf(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, st)
}

// eligible refuses the accounts that should not earn from this.
func (h *Handlers) eligible(account user.User) error {
	if account.Status != user.StatusActive {
		return httpx.ForbiddenCode("account_inactive", "This account cannot check in.")
	}
	if !account.EmailVerified && h.VerificationRequired != nil && h.VerificationRequired() {
		return httpx.ForbiddenCode("email_unverified", "Confirm your email address before checking in.")
	}
	return nil
}

func (h *Handlers) checkIn(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if err := h.eligible(account); err != nil {
		return err
	}
	res, err := h.service.CheckIn(r.Context(), account.ID)
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handlers) claim(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if err := h.eligible(account); err != nil {
		return err
	}
	reward, err := h.service.Claim(r.Context(), account.ID, r.PathValue("rule"))
	if err != nil {
		return translate(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"reward": reward})
}

// TranslateError maps this package's sentinels onto responses; exported so the
// administrative half answers the same way about the same failures.
func TranslateError(err error) error { return translate(err) }

func translate(err error) error {
	switch {
	case errors.Is(err, ErrDisabled):
		return httpx.ForbiddenCode("checkin_disabled", "Check-in is not switched on.")
	case errors.Is(err, ErrAlreadyToday):
		return httpx.Conflict("already_checked_in", "You have already checked in today.")
	case errors.Is(err, ErrNoSuchRule):
		return httpx.NotFound("There is no such milestone.")
	case errors.Is(err, ErrNotReached):
		return httpx.Conflict("milestone_not_reached", "You have not reached that milestone yet.")
	case errors.Is(err, ErrAlreadyClaimd):
		return httpx.Conflict("milestone_claimed", "You have already claimed that milestone.")
	case errors.Is(err, ErrInvalid):
		return httpx.BadRequest("%s", err.Error())
	}
	return httpx.Internal(err)
}
