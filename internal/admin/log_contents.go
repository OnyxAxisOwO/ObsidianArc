package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

func (h *Handlers) logContentSettings(w http.ResponseWriter, r *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": h.settings.Bool(settings.APIRequestContentEnabled)})
}

func (h *Handlers) setLogContentSettings(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4096); err != nil {
		return err
	}
	if body.Enabled == nil {
		return httpx.BadRequest("Give enabled.")
	}
	value := "false"
	if *body.Enabled {
		value = "true"
	}
	if err := h.settings.Set(r.Context(), settings.APIRequestContentEnabled, value); err != nil {
		return httpx.Internal(err)
	}
	return h.logContentSettings(w, r)
}

func (h *Handlers) logContent(w http.ResponseWriter, r *http.Request) error {
	key := r.PathValue("id")
	if !id.Valid(key) {
		return httpx.BadRequest("Malformed log id.")
	}
	content, err := h.requests.Content(r.Context(), key)
	if database.IsNotFound(err) {
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"body": "", "truncated": false})
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, content)
}

func (h *Handlers) pruneLogContents(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Days *int `json:"days"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4096); err != nil {
		return err
	}
	if body.Days == nil || *body.Days < 0 || *body.Days > 3650 {
		return httpx.BadRequest("Give days between 0 and 3650; zero clears all recorded content.")
	}
	removed, err := h.requests.PruneContents(r.Context(), *body.Days)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed})
}
