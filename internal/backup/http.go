package backup

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

const (
	// An export is streamed a conversation at a time, so its memory is bounded
	// by one conversation rather than the account. It still holds its slot for
	// as long as the download takes, and a slow reader keeps the slot. An import
	// holds a document of up to 32 MiB decoded into several times that. A
	// handful at once is a normal afternoon; a hundred accounts doing it
	// together is an out-of-memory kill for everybody else.
	maxConcurrentExports = 2
	maxConcurrentImports = 4
)

type Handlers struct {
	service *Service

	// What is running right now. In this process's memory and behind a mutex,
	// unlike the stored-message ceiling, because the thing being limited is
	// this process's memory: a second instance has its own heap and its own
	// allowance, and there is nothing in the database for a lock to protect.
	mu      sync.Mutex
	busy    map[string]struct{} // accounts with an import or export in flight
	exports int
	imports int
}

func NewHandlers(service *Service) *Handlers {
	return &Handlers{service: service, busy: map[string]struct{}{}}
}

// enter claims the account's one slot and a place under the global ceiling
// for that kind of work, and returns what gives both back.
//
// One at a time per account, import and export together: a second request
// from the same account is not another person, and the first is already the
// whole of what it asked for.
func (h *Handlers) enter(w http.ResponseWriter, accountID string, running *int, limit int) (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, taken := h.busy[accountID]; taken {
		w.Header().Set("Retry-After", "30")
		return nil, httpx.TooManyRequests("backup_in_progress",
			"An import or export is already running for this account. Wait for it to finish.")
	}
	if *running >= limit {
		w.Header().Set("Retry-After", "30")
		return nil, httpx.TooManyRequests("backup_busy",
			"The server is busy with other exports and imports. Try again in a minute.")
	}
	h.busy[accountID] = struct{}{}
	*running++

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.busy, accountID)
			*running--
		})
	}, nil
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}

	mux.Handle("GET /api/account/export", protected(h.export))
	mux.Handle("POST /api/account/import", protected(h.importDocument))
}

func (h *Handlers) export(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	release, err := h.enter(w, account.ID, &h.exports, maxConcurrentExports)
	if err != nil {
		return err
	}
	// Held until the last byte is written: the conversations are read while
	// the body streams, so the slot covers the whole download.
	defer release()

	// Measured before the response starts, so a refusal or a failed read is still
	// an error response rather than a body that begins and stops. The file is
	// named only once the body is about to begin: an error response carrying an
	// attachment name would be saved to disk as the export.
	stream, err := h.service.openExport(r.Context(), account)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			// 409, as the import answers a full account: the account is what is
			// too large, and sending the same request again will not change it.
			return httpx.Conflict("export_too_large", fmt.Sprintf(
				"This account is too large for one export file: the server imports at most %d conversations, %d messages and %d MiB, and a larger file would be refused on the way back in. Delete some conversations and export again.",
				MaxConversations, MaxMessagesPerImport, MaxDocumentBytes>>20))
		}
		return httpx.Internal(err)
	}

	// Named so a browser saving it produces something recognisable a year
	// later, rather than "export.json" among nine others.
	filename := "obsidian-arc-" + safeName(account.Username) + "-" +
		time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	return stream.writeTo(r.Context(), w)
}

func (h *Handlers) importDocument(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	// Before the body is read, not after: the decode is the expensive part.
	release, err := h.enter(w, account.ID, &h.imports, maxConcurrentImports)
	if err != nil {
		return err
	}
	defer release()

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
		case errors.Is(err, ErrNULCharacter):
			return httpx.BadRequest("That export contains a NUL character (U+0000), which this server does not store.")
		case errors.Is(err, ErrStorageFull):
			// 409 rather than 400: the document is fine and sending it again
			// will not help. Something has to be deleted first.
			//
			// The counts travel with it because the ceiling is no longer only
			// checked before anything is written — a concurrent writer can
			// fill the account between the first check and a later
			// conversation's transaction. A reader told plainly that nothing
			// was imported would re-send a file that is already partly in,
			// and get a second copy of whatever did land.
			return httpx.Conflict("storage_full",
				"This account is already storing as many messages as it may. Delete some conversations and try again.").
				WithDetails(map[string]any{
					"conversations": result.Conversations,
					"messages":      result.Messages,
					"skipped":       result.Skipped,
					"preferences":   result.Preferences,
				})
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
