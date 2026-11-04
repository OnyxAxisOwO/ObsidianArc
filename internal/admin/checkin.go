package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/checkin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Both handlers call h.Checkin without asking whether there is one: Routes
// mounts them only when there is, so a server without the service answers
// these paths 404 before either runs. Mount them anywhere else and that
// guard goes with them.

func (h *Handlers) checkinSettings(w http.ResponseWriter, _ *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, h.Checkin.Current())
}

func (h *Handlers) saveCheckinSettings(w http.ResponseWriter, r *http.Request) error {
	var body checkin.Settings
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}
	if err := h.Checkin.Save(r.Context(), body); err != nil {
		return checkin.TranslateError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, h.Checkin.Current())
}
