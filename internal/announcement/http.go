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
	mux.Handle("GET /api/announcements/{id}", auth.RequireUser(httpx.Wrap(h.show)))
	mux.Handle("POST /api/announcements/read", auth.RequireUser(httpx.Wrap(h.markSeen)))
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

	popup := choosePopup(records)
	if popup != nil {
		// The list carries no bodies, but the popup is the one the reader is
		// about to read, so it is the one body sent with the list. ByID does not
		// filter on published, so a row unpublished between the two reads would
		// otherwise come back with its body.
		full, err := h.store.ByID(r.Context(), popup.ID)
		switch {
		case errors.Is(err, ErrNotFound), err == nil && !full.Published:
			popup = nil
		case err != nil:
			return httpx.Internal(err)
		default:
			popup.Body = full.Body
		}
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"announcements": records,
		"unread":        unread,
		"popup":         popup,
	})
}

// show is one published announcement with its body, for the sheet a reader
// opens from the list. A draft is not found, as an unknown id is not.
func (h *Handlers) show(w http.ResponseWriter, r *http.Request) error {
	record, err := h.store.ByID(r.Context(), r.PathValue("id"))
	if err != nil {
		return TranslateError(err)
	}
	if !record.Published {
		return TranslateError(ErrNotFound)
	}
	return httpx.WriteJSON(w, http.StatusOK, record)
}

// choosePopup is the one announcement to put in front of the reader now. An
// unread "once" notice goes first: it is finite and stops being asked for the
// moment it is read. An "always" notice repeats on every visit, so it yields
// to a finite one for this visit and comes back on the next. Silent notices
// never pop up. Within each class the listed order holds, so the newest
// pinned-first row wins.
//
// Only one popup is returned per load, so of two "always" notices only the
// first in order is ever offered: the second waits until the first is
// unpublished or moved. Queueing popups would need the feed to carry a list,
// which the bell does not yet read.
func choosePopup(records []Announcement) *Announcement {
	var always *Announcement
	for i := range records {
		record := records[i]
		switch record.DisplayMode {
		case DisplayOnce:
			if !record.Read {
				return &record
			}
		case DisplayAlways:
			if always == nil {
				always = &record
			}
		}
	}
	return always
}

func (h *Handlers) markRead(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	announcementID := r.PathValue("id")
	if announcementID == "" {
		return httpx.BadRequest("An announcement id is required.")
	}

	// The store answers ErrNotFound for both an unknown id and a draft, so the
	// reader cannot tell them apart and a draft is never written as read.
	if err := h.store.MarkRead(r.Context(), account.ID, announcementID); err != nil {
		return TranslateError(err)
	}
	return httpx.NoContent(w)
}

// The body names the announcements the bell rendered. Marking by what the
// reader was sent, not by what is published at the moment of the click, keeps
// an announcement that arrived after the list was fetched unread until it is
// shown. The cap covers about nine thousand announcements: a ULID with its
// quotes and comma is 29 bytes, so 256 KiB holds about that many. ListFor is
// unpaged, so past that a mark-all is refused, and the bell then reloads
// without marking anything. The cap has to be raised if the history grows
// past it.
func (h *Handlers) markSeen(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 256*1024); err != nil {
		return err
	}
	if err := h.store.MarkSeen(r.Context(), account.ID, body.IDs); err != nil {
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
