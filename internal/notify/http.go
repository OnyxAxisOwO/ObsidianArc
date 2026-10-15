package notify

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Handlers is the browser's own inbox. Nothing here is administrative — an
// operator notice reaches an account only because the store's visibility
// rules already say it may — so every route sits behind auth.RequireUser
// alone, the same as the feedback and announcement feeds.
type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/notifications", auth.RequireUser(httpx.Wrap(h.list)))
	// Before {id}-shaped routes would matter is not a concern here — there is
	// no path parameter — but it is named apart from the collection route for
	// the same reason feedback's "unread" is: a poll is not a page of the
	// list, it is its own question asked every thirty seconds.
	mux.Handle("GET /api/notifications/poll", auth.RequireUser(httpx.Wrap(h.poll)))
	mux.Handle("POST /api/notifications/read", auth.RequireUser(httpx.Wrap(h.read)))
}

const maxReadBody = 4 * 1024

type readRequest struct {
	// Zero (or an absent body) means "now", which is what "mark all read"
	// sends: the whole point of that click is not having to say when now is.
	// Store.MarkRead clamps this to the server's own clock either way, so a
	// value here can move the watermark forward but never past what has
	// actually happened yet.
	UpTo int64 `json:"up_to"`
}

// parseTimestamp reads an optional millisecond timestamp from a query
// parameter, refusing anything present but unparsable rather than silently
// treating a typo as zero.
func parseTimestamp(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, httpx.BadRequest("%s must be a timestamp in milliseconds.", name)
	}
	return value, nil
}

// parseLimit reads the page size, refusing anything present but unparsable.
// Store.List clamps an out-of-range value on its own, so 0 (unset) is passed
// straight through.
func parseLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, httpx.BadRequest("limit must be a whole number.")
	}
	return value, nil
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	limit, err := parseLimit(r)
	if err != nil {
		return err
	}
	before, err := parseTimestamp(r, "before")
	if err != nil {
		return err
	}

	records, err := h.store.List(r.Context(), account, limit, before)
	if err != nil {
		return httpx.Internal(err)
	}
	seenAt, unread, err := h.badge(r, account)
	if err != nil {
		return err
	}
	// The server's own clock, alongside seen_at: a client seeds its poll
	// cursor from this rather than from the newest row's own created_at, or
	// an account whose visibility widens later (promoted to admin) would see
	// its whole new backlog raised as toasts on the very next poll.
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"notifications": records, "unread": unread, "seen_at": seenAt, "now": time.Now().UnixMilli(),
	})
}

func (h *Handlers) poll(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	after, err := parseTimestamp(r, "after")
	if err != nil {
		return err
	}

	records, err := h.store.Poll(r.Context(), account, after)
	if err != nil {
		return httpx.Internal(err)
	}
	_, unread, err := h.badge(r, account)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"notifications": records, "unread": unread, "now": time.Now().UnixMilli(),
	})
}

func (h *Handlers) read(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	var body readRequest
	// Content-Length cannot tell an empty body from a chunked one, which reports
	// -1 whatever it carries, so the body is asked directly. Only io.EOF means
	// absent: any other read error goes to DecodeJSON and is answered as a bad
	// request, rather than quietly marking everything read.
	br := bufio.NewReader(r.Body)
	if _, err := br.Peek(1); !errors.Is(err, io.EOF) {
		// Peek leaves the byte buffered in br, so wrapping br hands DecodeJSON
		// the whole body and not one that is missing its first character.
		r.Body = io.NopCloser(br)
		if err := httpx.DecodeJSON(w, r, &body, maxReadBody); err != nil {
			return err
		}
	}
	if err := h.store.MarkRead(r.Context(), account.ID, body.UpTo); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

// badge is the unread count every response above carries, always measured
// against the account's own read watermark rather than whatever window the
// caller happened to ask List or Poll for — the bell's number and the rows on
// screen answer two different questions.
func (h *Handlers) badge(r *http.Request, account user.User) (seenAt int64, unread int, err error) {
	seenAt, err = h.store.SeenAt(r.Context(), account.ID)
	if err != nil {
		return 0, 0, httpx.Internal(err)
	}
	unread, err = h.store.Unread(r.Context(), account, seenAt)
	if err != nil {
		return 0, 0, httpx.Internal(err)
	}
	return seenAt, unread, nil
}
