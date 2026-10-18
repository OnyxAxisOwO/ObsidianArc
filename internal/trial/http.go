package trial

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

type Handlers struct {
	settings *settings.Service
	models   *model.Store
	registry *adapter.Registry
	trust    httpx.ProxyTrust
	budget   *budget
	signer   *signer
}

func NewHandlers(
	set *settings.Service,
	models *model.Store,
	registry *adapter.Registry,
	trust httpx.ProxyTrust,
	secret []byte,
) *Handlers {
	return &Handlers{
		settings: set,
		models:   models,
		registry: registry,
		trust:    trust,
		budget:   newBudget(),
		signer:   newSigner(secret),
	}
}

// Routes mounts the one public endpoint. Public on purpose — the whole point
// is a visitor with no account — which is why every limit it has is checked
// inside the handler rather than delegated to middleware that is not there.
func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/trial/chat", httpx.Wrap(h.chat))
}

// The body cap is sized from the character limit, not from typical text. A
// conversation may hold MaxTotalChars runes, and a rune written as a \u escape
// (a control or HTML character, which Go's encoder always escapes, or a BMP
// character a client chose to escape) takes six bytes, so a cap smaller than
// that refuses legitimate conversations in languages written in multi-byte
// characters. A supplementary-plane character escaped as a surrogate pair takes
// twelve: a client that escapes every one of those is refused at the cap rather
// than the cap being raised for everyone. The browser sends them as raw UTF-8,
// four bytes. The rest is room for the framing of at most maxTrialMessages
// messages and the continuation.
//
// Validation runs before the budget, so a turn refused for its shape costs no
// allowance. The price is that a turn the budget refuses has already been read
// up to this cap.
const maxTrialBodyBytes = 6*MaxTotalChars + 16<<10

// At the turn cap a conversation holds 2*MaxTrialTurns-1 messages: a question
// and an answer for each earlier turn, then the last question. The limit allows
// one extra question and answer beyond the cap, so the framing the body cap
// leaves room for stays bounded.
const maxTrialMessages = 2*settings.MaxTrialTurns + 1

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Messages []message `json:"messages"`
	// Handed back by the previous turn, signed. Absent on the first.
	Continuation string `json:"continuation"`
}

func (h *Handlers) chat(w http.ResponseWriter, r *http.Request) error {
	// Off unless the front door is the chat and the trial is switched on. Two
	// settings rather than one, so turning the landing page back to the
	// sign-in card also stops the spending.
	if h.settings.Get(settings.LandingMode) != settings.LandingChat ||
		!h.settings.Bool(settings.TrialEnabled) {
		return httpx.NotFound("No such endpoint.")
	}

	var body request
	if err := httpx.DecodeJSON(w, r, &body, maxTrialBodyBytes); err != nil {
		return err
	}

	turns, err := h.validate(body)
	if err != nil {
		return err
	}

	// A bound on open upstream connections rather than on cost, so a burst at
	// the front door cannot starve the accounts paying for this instance. Taken
	// with the charge in budget.admit, slot first.
	address := httpx.ClientIP(r, h.trust)
	done, retryAfter, refused := h.budget.admit(address)
	switch refused {
	case refusedBusy:
		return httpx.TooManyRequests("trial_busy",
			"The trial is busy right now. Try again in a moment.")
	case refusedExhausted:
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		return httpx.TooManyRequests("trial_exhausted",
			"You have used the trial for now. Create an account to keep going.")
	}
	defer done()

	// The lookup is a database read, so it runs after the in-memory refusals:
	// a flood turned away by the budget must not cost a query. A turn with no
	// model to send it to gives its charge back, since it never reaches the
	// provider.
	resolved, err := h.resolveModel(r)
	if err != nil {
		h.budget.refund(address)
		return err
	}

	messages := make([]adapter.Message, 0, len(body.Messages))
	for _, entry := range body.Messages {
		role := adapter.RoleUser
		if entry.Role == string(adapter.RoleAssistant) {
			role = adapter.RoleAssistant
		}
		messages = append(messages, adapter.Message{
			Role:  role,
			Parts: []adapter.Part{{Kind: adapter.PartText, Text: entry.Content}},
		})
	}

	sse, err := httpx.NewSSE(w)
	if err != nil {
		// Nothing has reached the provider, so the charge goes back, as it does
		// for a turn with no model.
		h.budget.refund(address)
		return httpx.Internal(err)
	}

	sink := func(event adapter.Event) error {
		if event.Type == adapter.EventDelta {
			return sse.Event("delta", map[string]any{"text": event.Text})
		}
		// Everything else is dropped, reasoning included: a trial is a sample of
		// the answer, and the reasoning of a model the visitor did not choose is
		// noise.
		return nil
	}

	// No reasoning, no system prompt beyond the instance's own, no images:
	// the trial is the narrowest request this server knows how to make.
	result, chatErr := h.registry.Chat(r.Context(), resolved.Provider, adapter.ChatRequest{
		Model:    resolved.Upstream.Spec(),
		System:   h.settings.Get(settings.DefaultSystemPrompt),
		Messages: messages,
		// The trial's own ceiling, not the model's: the model is the
		// operator's choice, and how much of it a stranger may ask them to buy
		// is a different question.
		MaxTokens: trialMaxTokens(resolved.Upstream.MaxOutputTokens),
		Stream:    true,
	}, sink)

	// Trial spending is the operator's, against no account, so it cannot go
	// in the usage ledger — every row there belongs to a user. It is logged
	// instead, which is where an operator would look for "what is the front
	// door costing me".
	slog.InfoContext(r.Context(), "trial turn",
		"address", address,
		"model", resolved.Model.DisplayName,
		"turns", turns,
		"input_tokens", result.Usage.InputTokens,
		"output_tokens", result.Usage.OutputTokens,
		"error", chatErr)

	if chatErr != nil {
		if r.Context().Err() != nil {
			return nil
		}
		_ = sse.Event("error", map[string]any{
			"code":    "upstream",
			"message": "That did not work. Try again in a moment.",
		})
		return nil
	}
	// The count goes back signed, and comes back with the next question. It
	// is the only reason the turn limit means anything: the exchange itself
	// is written by the client, so a client that simply forgets the earlier
	// turns would otherwise look like a first-time visitor forever.
	return sse.Event("done", map[string]any{
		"turns_left":   h.remaining(turns),
		"continuation": h.signer.issue(turns, time.Now()),
	})
}

func trialMaxTokens(modelCeiling int) int {
	if modelCeiling > 0 && modelCeiling < MaxTrialOutputTokens {
		return modelCeiling
	}
	return MaxTrialOutputTokens
}

// validate enforces the shape and the turn cap.
//
// The count comes from the signed continuation, not from the messages: the
// client writes the exchange, so counting what it sent would count a number
// it chose. What the messages are still checked for is size — a trial must
// not be a way to post a megabyte of prompt.
func (h *Handlers) validate(body request) (int, error) {
	if len(body.Messages) == 0 {
		return 0, httpx.BadRequestCode("trial_empty", "Nothing to send.")
	}
	if len(body.Messages) > maxTrialMessages {
		return 0, httpx.BadRequestCode("trial_too_long", "That conversation is too long for a trial.")
	}

	previous, err := h.signer.verify(body.Continuation)
	if err != nil {
		return 0, httpx.ForbiddenCode("trial_finished",
			"That is the end of the trial. Create an account to keep going.")
	}
	turns := previous + 1

	total := 0
	for _, entry := range body.Messages {
		if strings.TrimSpace(entry.Content) == "" {
			return 0, httpx.BadRequestCode("trial_empty", "Nothing to send.")
		}
		if len([]rune(entry.Content)) > MaxMessageChars {
			return 0, httpx.BadRequestCode("trial_message_too_long", "Message must be %d characters or fewer.", MaxMessageChars)
		}
		total += len([]rune(entry.Content))
	}
	if total > MaxTotalChars {
		return 0, httpx.BadRequestCode("trial_too_long", "That conversation is too long for a trial.")
	}
	if body.Messages[len(body.Messages)-1].Role == string(adapter.RoleAssistant) {
		return 0, httpx.BadRequestCode("trial_empty", "Nothing to send.")
	}

	if turns > h.allowedTurns() {
		return 0, httpx.ForbiddenCode("trial_finished",
			"That is the end of the trial. Create an account to keep going.")
	}
	return turns, nil
}

func (h *Handlers) allowedTurns() int {
	turns := h.settings.Int(settings.TrialTurns, 3)
	if turns < 1 {
		return 1
	}
	if turns > settings.MaxTrialTurns {
		return settings.MaxTrialTurns
	}
	return turns
}

func (h *Handlers) remaining(used int) int {
	left := h.allowedTurns() - used
	if left < 0 {
		return 0
	}
	return left
}

// resolveModel picks the model the operator nominated for the front door, and
// refuses when they nominated none.
//
// Authorize is called as an administrator on purpose: there is no account and
// therefore no group, and the permission that matters was granted when an
// operator chose this model for the front door. That is also why there is no
// "first enabled model" fallback: as an administrator it walks past every
// group restriction, so a visitor with no account could have been served
// whichever enabled model happened to be listed first, whatever it costs and
// whoever it was meant for, merely because nobody had chosen. It still
// enforces that the model and its provider are enabled, and it still resolves
// routing.
func (h *Handlers) resolveModel(r *http.Request) (model.Resolved, error) {
	modelID := strings.TrimSpace(h.settings.Get(settings.TrialModel))
	if modelID == "" {
		return model.Resolved{}, httpx.UnavailableCode("trial_unavailable", "No model is available right now.")
	}

	resolved, err := h.models.Authorize(r.Context(), "", modelID, true)
	if err != nil {
		return model.Resolved{}, model.TranslateError(err)
	}
	return resolved, nil
}
