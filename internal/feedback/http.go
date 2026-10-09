package feedback

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
)

// Handlers is the author's half: send one, read the conversation on it, and
// answer what an operator said back. Reading anybody else's, changing a
// status and deleting live in internal/admin, on the same terms every other
// operator surface does.
//
// Every route here is scoped to the caller by the store's queries rather than
// by a check above them, so somebody else's thread is reported as absent —
// which is also all the query knows.
type Handlers struct {
	store *Store
	// The human check, where the operator has switched it on. It guards both
	// writes an account can make here — filing a report and answering one —
	// because the cap on reports is ten a day while a thread holds fifty
	// turns, so replies are the cheaper door of the two and gating only the
	// expensive one would have secured the wrong half.
	Challenge turnstile.Gate
	// Resolves the caller's address for the challenge, set by the wiring the
	// way apikey.Handlers.ClientIP is. A nil ClientIP simply means Turnstile
	// is asked without one.
	ClientIP func(*http.Request) string
	// Whether a reader is told which operator answered them. Nil means yes,
	// which is the setting's own default. When it says no the name is
	// removed here rather than hidden by the screen: a name the client is
	// sent is a name anybody can read out of the response.
	ShowStaffName func() bool
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/feedback", auth.RequireUser(httpx.Wrap(h.list)))
	mux.Handle("POST /api/feedback", auth.RequireUser(httpx.Wrap(h.create)))
	// Before the {id} route, or "unread" would be read as one.
	mux.Handle("GET /api/feedback/unread", auth.RequireUser(httpx.Wrap(h.unread)))
	mux.Handle("GET /api/feedback/{id}", auth.RequireUser(httpx.Wrap(h.thread)))
	mux.Handle("POST /api/feedback/{id}/replies", auth.RequireUser(httpx.Wrap(h.reply)))
}

// Generous next to MaxBodyChars, so a report that is a little too long is
// refused with a sentence about its length rather than by the decoder.
const maxBody = 64 * 1024

type writeRequest struct {
	Kind     string `json:"kind"`
	Priority string `json:"priority"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	// The Turnstile token, where the operator has switched the challenge on
	// for this scene. Ignored when they have not.
	Turnstile string `json:"turnstile"`
}

type listResponse struct {
	Feedback []Feedback `json:"feedback"`
	// What the form needs to tell somebody they have run out for today,
	// before they have written five hundred words into a box.
	Remaining int `json:"remaining"`
	MaxPerDay int `json:"max_per_day"`
}

type replyRequest struct {
	Body string `json:"body"`
	// The same challenge the report itself passes, for the same reason: a
	// thread holds fifty turns, so replying is a write an automated caller
	// can repeat far more often than it can file new reports. Gating the
	// report and not the replies would have left the cheaper door open.
	Turnstile string `json:"turnstile"`
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	records, err := h.store.ListFor(r.Context(), nil, account.ID)
	if err != nil {
		return httpx.Internal(err)
	}

	// Counted from the rows already in hand rather than with a second query:
	// ListFor returns the newest fifty, and the day's ten are inside that.
	remaining := MaxPerDay
	cutoff := time.Now().UnixMilli() - DayWindow.Milliseconds()
	for _, record := range records {
		if record.CreatedAt > cutoff {
			remaining--
		}
	}
	if remaining < 0 {
		remaining = 0
	}

	return httpx.WriteJSON(w, http.StatusOK, listResponse{
		Feedback: records, Remaining: remaining, MaxPerDay: MaxPerDay,
	})
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	var body writeRequest
	if err := httpx.DecodeJSON(w, r, &body, maxBody); err != nil {
		return err
	}

	// Before the write, so a refused challenge costs nothing and spends none
	// of the sender's daily allowance.
	if err := h.Challenge.Check(r.Context(), body.Turnstile, h.addressOf(r)); err != nil {
		return challengeError(err)
	}

	record, err := h.store.Create(r.Context(), account.ID, Input{
		Kind:     Kind(body.Kind),
		Priority: Priority(body.Priority),
		Title:    body.Title,
		Body:     body.Body,
	})
	if err != nil {
		return TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, record)
}

// GET /api/feedback/{id} — one of the caller's own threads, and opening it
// is what "I have read the answer" means. The flag is cleared on the read
// rather than by a second call the client has to remember to make: there is
// no other reason to fetch a thread, and a client that forgot would leave a
// dot on somebody's menu for good.
func (h *Handlers) thread(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	thread, err := h.store.Thread(r.Context(), nil, r.PathValue("id"), account.ID)
	if err != nil {
		return TranslateError(err)
	}
	if h.ShowStaffName != nil && !h.ShowStaffName() {
		for i := range thread.Replies {
			if thread.Replies[i].FromStaff {
				thread.Replies[i].Username = ""
				thread.Replies[i].Nickname = ""
				// The id names the operator as surely as the name does.
				thread.Replies[i].UserID = ""
			}
		}
	}
	if thread.Feedback.AuthorUnread {
		// Detached, like every other write that happens after the answer is
		// decided: the reader has read it either way, and a tab closed in the
		// same breath must not undo that.
		if err := h.store.MarkSeen(context.WithoutCancel(r.Context()), thread.Feedback.ID, false); err != nil {
			return httpx.Internal(err)
		}
		thread.Feedback.AuthorUnread = false
	}
	return httpx.WriteJSON(w, http.StatusOK, thread)
}

// POST /api/feedback/{id}/replies — the author answering on their own thread.
func (h *Handlers) reply(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	var body replyRequest
	if err := httpx.DecodeJSON(w, r, &body, maxBody); err != nil {
		return err
	}

	// Before the write, so a refused challenge costs nothing and leaves the
	// thread as it was.
	if err := h.Challenge.Check(r.Context(), body.Turnstile, h.addressOf(r)); err != nil {
		return challengeError(err)
	}

	record, err := h.store.AddReply(r.Context(), ReplyInput{
		FeedbackID: r.PathValue("id"),
		UserID:     account.ID,
		Body:       body.Body,
		// The author's own turn, whatever else this account may be allowed to
		// do elsewhere: an administrator writing on their own report is
		// speaking as the person who filed it.
		FromStaff:    false,
		RequireOwner: true,
	})
	if err != nil {
		return TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, record)
}

// GET /api/feedback/unread — the number behind the dot on the account menu.
// Its own endpoint because the menu is on every page and the list is not.
func (h *Handlers) unread(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	count, err := h.store.UnreadFor(r.Context(), nil, account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"unread": count})
}

func (h *Handlers) addressOf(r *http.Request) string {
	if h.ClientIP == nil {
		return ""
	}
	return h.ClientIP(r)
}

// challengeError says what the sign-up page and the key panel say, in the
// same codes, so one string in the client covers every screen that can draw
// the widget.
func challengeError(err error) error {
	switch {
	case errors.Is(err, turnstile.ErrFailed):
		return httpx.ForbiddenCode("challenge_failed",
			"The verification could not be completed. Try again.")
	case errors.Is(err, turnstile.ErrUnavailable):
		return httpx.UnavailableCode("challenge_unavailable",
			"Verification is unavailable right now. Try again shortly.")
	default:
		return httpx.Internal(err)
	}
}

// TranslateError maps this package's sentinels onto responses, so the admin
// handlers and these read the same way.
func TranslateError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("No such feedback.")
	case errors.Is(err, ErrInvalidTitle):
		return httpx.BadRequest("A title of 1-120 characters is required.")
	case errors.Is(err, ErrInvalidBody):
		return httpx.BadRequest("Please describe it in 1-8000 characters.")
	case errors.Is(err, ErrInvalidKind):
		return httpx.BadRequest("Feedback is either a bug or an idea.")
	case errors.Is(err, ErrInvalidPrio):
		return httpx.BadRequest("Priority must be low, medium or high.")
	case errors.Is(err, ErrInvalidStatus):
		return httpx.BadRequest("Status must be open or resolved.")
	case errors.Is(err, ErrThreadFull):
		return httpx.BadRequest("This conversation has reached its maximum length.")
	case errors.Is(err, ErrTooMany):
		return httpx.TooManyRequests("feedback_daily_limit",
			"You have sent today's maximum number of reports. Please continue tomorrow.")
	default:
		return httpx.Internal(err)
	}
}
