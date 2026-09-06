package backup

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

type Handlers struct{ service *Service }

func NewHandlers(service *Service) *Handlers { return &Handlers{service: service} }

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}

	mux.Handle("GET /api/account/export", protected(h.export))
	mux.Handle("POST /api/account/import", protected(h.importDocument))
}

func (h *Handlers) export(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	document, err := h.service.Export(r.Context(), account)
	if err != nil {
		return httpx.Internal(err)
	}

	// Named so a browser saving it produces something recognisable a year
	// later, rather than "export.json" among nine others.
	filename := "obsidian-arc-" + safeName(account.Username) + "-" +
		time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	return httpx.WriteJSON(w, http.StatusOK, document)
}

func (h *Handlers) importDocument(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	// Lenient: an export from a later release carries fields this build has
	// not heard of, and losing them is the right outcome — refusing the whole
	// file is not.
	var document Document
	if err := httpx.DecodeJSONLenient(w, r, &document, MaxDocumentBytes); err != nil {
		return err
	}

	result, err := h.service.Import(r.Context(), account, document)
	if err != nil {
		switch {
		case errors.Is(err, ErrWrongFormat):
			return httpx.BadRequest("That file is not an Obsidian Arc export.")
		case errors.Is(err, ErrTooLarge):
			return httpx.BadRequest(
				"That export is larger than this server will import: at most %d conversations and %d messages.",
				MaxConversations, MaxMessagesPerImport)
		case errors.Is(err, ErrStorageFull):
			// 409 rather than 400: the document is fine and sending it again
			// will not help. Something has to be deleted first.
			return httpx.Conflict("storage_full",
				"This account is already storing as many messages as it may. Delete some conversations and try again.")
		}
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, result)
}

// safeName keeps a username usable as a filename on every platform without
// pulling in a dependency for it.
func safeName(username string) string {
	var out strings.Builder
	for _, r := range username {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('-')
		}
	}
	name := strings.Trim(out.String(), "-")
	if name == "" {
		return "account"
	}
	return name
}
