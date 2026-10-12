package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

const (
	// An export is streamed a page of messages at a time, so its memory is
	// bounded by the page rather than by the conversation or the account. It
	// holds its slot for as long as the download takes, and a slow reader keeps
	// the slot; a reader that stops taking bytes is cut off by the write
	// deadline. An import
	// holds a document of up to 32 MiB decoded into several times that. A
	// handful at once is a normal afternoon; a hundred accounts doing it
	// together is an out-of-memory kill for everybody else.
	maxConcurrentExports = 2
	maxConcurrentImports = 4

	// The server sets no WriteTimeout, and a socket whose reader has stopped
	// taking bytes blocks a write indefinitely. The slot is held until the body
	// is written, so each write is armed with a deadline, as httpx's SSE does
	// per frame. Chunked, so a deadline is per 64 KiB rather than per whole
	// conversation: a slow reader that is still reading keeps its slot.
	exportWriteTimeout = 30 * time.Second
	exportWriteChunk   = 64 << 10

	// The body of an import is read while the import holds one of four slots,
	// and the server's own five-minute ReadTimeout is far too long for that. Two
	// minutes is ample for the largest document the import accepts on any
	// connection an operator would reasonably run.
	importBodyTimeout = 2 * time.Minute

	// Imports one account may start at once, and then one every importRefill.
	importBurst  = 5
	importRefill = 10 * time.Second
)

type Handlers struct {
	service *Service

	// Imports per account per time. Held in this process's memory for the same
	// reason as the slots below: it limits this process's work.
	importRate *httpx.TokenBucketLimiter

	// The gate every write an account makes must pass: an import writes a
	// conversation per thread, so it spends disk the same way a message or an
	// upload does. Wired to the same check as the chat, so an unconfirmed
	// address is refused here too. Nil admits everyone.
	Writable func(ctx context.Context, account user.User) error

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
	return &Handlers{
		service:    service,
		busy:       map[string]struct{}{},
		importRate: httpx.NewTokenBucketLimiter(1/importRefill.Seconds(), importBurst),
	}
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

	// A failed write is left with its deadline in the past: the connection is
	// broken and the server closes it, so nothing more is sent on it.
	rc := http.NewResponseController(w)
	if err := stream.writeTo(r.Context(), &deadlineWriter{w: w, rc: rc, timeout: exportWriteTimeout}); err != nil {
		return err
	}
	_ = rc.SetWriteDeadline(time.Time{})
	return nil
}

// deadlineWriter arms a write deadline before each chunk it passes on. A
// writer without deadline support (a test recorder, the console's in-process
// writer) is written to unchanged: the controller reports that it cannot set
// one, and the write goes ahead.
type deadlineWriter struct {
	w       io.Writer
	rc      *http.ResponseController
	timeout time.Duration
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > exportWriteChunk {
			chunk = chunk[:exportWriteChunk]
		}
		_ = d.rc.SetWriteDeadline(time.Now().Add(d.timeout))
		n, err := d.w.Write(chunk)
		written += n
		if err != nil {
			return written, err
		}
		p = p[len(chunk):]
	}
	return written, nil
}

func (h *Handlers) importDocument(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	// Before the slot is taken, so a refused account does not hold one of the
	// four, and before the body is read: the decode is the expensive part.
	if h.Writable != nil {
		if err := h.Writable(r.Context(), account); err != nil {
			return err
		}
	}

	// Every import reads the account's stored text, whatever it writes, so a
	// repeated one is a repeated scan of up to 512 MiB. A few at once is a
	// person clicking through a file; a steady stream is not.
	if !h.importRate.Allow(account.ID) {
		w.Header().Set("Retry-After", strconv.Itoa(int(importRefill/time.Second)))
		return httpx.TooManyRequests("import_rate_limited",
			"Too many imports in a short time. Wait a few seconds and try again.")
	}

	release, err := h.enter(w, account.ID, &h.imports, maxConcurrentImports)
	if err != nil {
		return err
	}
	defer release()

	// Armed after the slot is held, so the time spent waiting for one does not
	// count against the body. Deliberately not cleared once the decode returns:
	// when the handler is done the server reads whatever is left of the body,
	// so that it can reuse the connection, and with the deadline gone that read
	// has no bound at all. A client that sent a complete document and then
	// stopped would hold its goroutine and descriptor for ever. The next request
	// on the connection resets the deadline, so nothing outlives this one.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(importBodyTimeout))

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
