package announcement

import (
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers is the reader's half: the list behind the bell, the one that
// should pop up now, and marking things read. Writing them lives in
// internal/admin.
type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/announcements", auth.RequireUser(httpx.Wrap(h.list)))
	mux.Handle("POST /api/announcements/read", auth.RequireUser(httpx.Wrap(h.markAllRead)))
	mux.Handle("POST /api/announcements/{id}/read", auth.RequireUser(httpx.Wrap(h.markRead)))
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	records, err := h.store.ListFor(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}

	unread := 0
	for _, record := range records {
		if !record.Read {
			unread++
		}
	}

	// Which one, if any, the client should put in front of the reader right
	// now. Decided here rather than in the browser so the rule is one
	// sentence in one place: the first announcement that still wants to be
	// seen, in the order they are listed.
	var popup *Announcement
	for i := range records {
		record := records[i]
		if record.DisplayMode == DisplaySilent {
			continue
		}
		// "once" has had its turn as soon as it is read; "always" keeps
		// asking, and the browser is what remembers it has already asked
		// during this visit.
		if record.DisplayMode == DisplayOnce && record.Read {
			continue
		}
		popup = &record
		break
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"announcements": records,
		"unread":        unread,
		"popup":         popup,
	})
}

func (h *Handlers) markRead(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	announcementID := r.PathValue("id")
	if announcementID == "" {
		return httpx.BadRequest("An announcement id is required.")
	}

	// Through ByID first, so marking an id that does not exist is a 404
	// rather than a foreign-key error out of the insert.
	if _, err := h.store.ByID(r.Context(), announcementID); err != nil {
		return TranslateError(err)
	}
	if err := h.store.MarkRead(r.Context(), account.ID, announcementID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) markAllRead(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if err := h.store.MarkAllRead(r.Context(), account.ID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

// TranslateError maps this package's sentinels onto responses, so the admin
// handlers and these read the same way.
func TranslateError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound("No such announcement.")
	case errors.Is(err, ErrInvalidTitle):
		return httpx.BadRequest("Title must be 1-120 characters.")
	case errors.Is(err, ErrInvalidBody):
		return httpx.BadRequest("That announcement is too long.")
	case errors.Is(err, ErrInvalidMode):
		return httpx.BadRequest("Unknown display mode.")
	default:
		return httpx.Internal(err)
	}
}
