// The bot's webhook: the one externally reachable way to process a group
// departure. The QQ group chat is outside this instance's reach, so the bot
// that does see it calls in here when a member leaves, carrying nothing but
// the QQ number it watched walk out.
//
// The route sits on the public mux with no session and no account — the
// bearer token is the whole gate. That is why it is deliberately narrow: one
// event type, one account resolution (by QQ), and a fixed set of outcomes,
// each a plain status word the bot can relay to the group chat without
// parsing anything subtler.
package invite

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type BotHandlers struct {
	store *Store
	// The caller's address as the proxy settings resolve it, for the
	// security log and the rate limit's key. Nil records none.
	ClientIP func(*http.Request) string
	// Caps how fast one address can process departures — a buggy bot
	// looping on one event must not turn into a write storm. Checked after
	// the token, so an unauthenticated flood is refused at the constant-
	// time comparison without ever reaching this or the database.
	Limiter *httpx.TokenBucketLimiter
}

func NewBotHandlers(store *Store) *BotHandlers { return &BotHandlers{store: store} }

func (h *BotHandlers) Routes(mux *http.ServeMux) {
	mux.Handle("POST /api/bot/departure", httpx.Wrap(h.departure))
}

type botDepartureRequest struct {
	QQ   string `json:"qq"`
	Mode string `json:"mode"`
}

// departure processes one member leaving. Every outcome short of a transport
// error is a 200 with a status word — the bot has already done its job by
// reporting the event, and "unknown QQ" is a normal Tuesday, not a failure.
// Only a bad token is a 401, and every bad token reads the same whether the
// route is switched off or the token is wrong: which one it is says nothing
// an attacker could use.
func (h *BotHandlers) departure(w http.ResponseWriter, r *http.Request) error {
	expected := strings.TrimSpace(h.store.settings.Get(settings.BotWebhookToken))
	provided := bearerToken(r)
	if expected == "" ||
		subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		return httpx.UnauthorizedCode("bot_unauthorized", "This endpoint needs a valid bearer token.")
	}

	// Rate limit goes inside the token: the comparison above is what a
	// guessing attacker gets, and this is what a holder of the real token
	// — a looping or retrying bot — is bounded by.
	if h.Limiter != nil {
		ip := ""
		if h.ClientIP != nil {
			ip = h.ClientIP(r)
		}
		if !h.Limiter.Allow("bot-departure:" + ip) {
			return httpx.TooManyRequests("rate_limited", "Too many departure reports from this address. Please try again later.")
		}
	}

	var body botDepartureRequest
	if err := httpx.DecodeJSON(w, r, &body, 1024); err != nil {
		return err
	}
	qq := strings.TrimSpace(body.QQ)
	if err := user.ValidateQQ(qq); err != nil || qq == "" {
		return httpx.BadRequestCode("bot_bad_qq", "A valid QQ number is required.")
	}
	// The event may name its own mode; otherwise the instance's default
	// decides. An unknown mode is refused rather than fallen back from, so
	// a misconfigured bot finds out immediately instead of watching every
	// report do something it did not mean.
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = h.store.settings.Get(settings.BotDepartureMode)
	}
	if !settings.ValidDepartMode(mode) {
		return httpx.BadRequestCode("bot_bad_mode", "Unknown departure mode %q.", mode)
	}

	ip := ""
	if h.ClientIP != nil {
		ip = h.ClientIP(r)
	}
	result, err := h.store.DepartByQQ(r.Context(), qq, DepartInput{
		Mode: mode, Source: SourceBot, IP: ip,
	})
	switch {
	case err == nil:
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "departed", "result": result,
		})
	case errors.Is(err, ErrDeparted):
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "already_departed", "result": result,
		})
	case errors.Is(err, user.ErrNotFound):
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "unknown", "result": result,
		})
	case errors.Is(err, ErrDepartAdmin):
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "refused", "result": result,
		})
	default:
		return httpx.Internal(err)
	}
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const scheme = "Bearer "
	if len(header) < len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(header[len(scheme):])
}
