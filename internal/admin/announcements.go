package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/announcement"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Writing announcements. Reading them is in internal/announcement, behind
// RequireUser; everything here is behind RequireAdmin by virtue of being
// mounted in Routes.

const maxAnnouncementBody = 64 * 1024

type announcementRequest struct {
	Title               string                   `json:"title"`
	Body                string                   `json:"body"`
	DisplayMode         announcement.DisplayMode `json:"display_mode"`
	DismissAfterSeconds int                      `json:"dismiss_after_seconds"`
	Published           bool                     `json:"published"`
	Pinned              bool                     `json:"pinned"`
}

func (b announcementRequest) input() announcement.Input {
	return announcement.Input{
		Title:               b.Title,
		Body:                b.Body,
		DisplayMode:         b.DisplayMode,
		DismissAfterSeconds: b.DismissAfterSeconds,
		Published:           b.Published,
		Pinned:              b.Pinned,
	}
}

func (h *Handlers) listAnnouncements(w http.ResponseWriter, r *http.Request) error {
	records, err := h.announcements.ListAll(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"announcements": records})
}

func (h *Handlers) createAnnouncement(w http.ResponseWriter, r *http.Request) error {
	var body announcementRequest
	if err := httpx.DecodeJSON(w, r, &body, maxAnnouncementBody); err != nil {
		return err
	}

	record, err := h.announcements.Create(r.Context(), body.input())
	if err != nil {
		return announcement.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"announcement": record})
}

func (h *Handlers) updateAnnouncement(w http.ResponseWriter, r *http.Request) error {
	announcementID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	var body announcementRequest
	if err := httpx.DecodeJSON(w, r, &body, maxAnnouncementBody); err != nil {
		return err
	}

	record, err := h.announcements.Update(r.Context(), announcementID, body.input())
	if err != nil {
		return announcement.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"announcement": record})
}

func (h *Handlers) deleteAnnouncement(w http.ResponseWriter, r *http.Request) error {
	announcementID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.announcements.Delete(r.Context(), announcementID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}
