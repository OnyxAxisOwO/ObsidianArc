package compat

import (
	"context"
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
)

// maxBodyPlaces is how many request bodies one account may be reading into
// memory at once. A /v1 body is held from its first byte until it is parsed.
// Chat bodies run to 12 MB and picture bodies to about 40 MB, so this count is
// what caps the memory an account holds while its bodies arrive. Four lets the
// calls an agent makes in parallel be read together; a fifth waits, and only
// for as long as the client takes to send one of the four.
const maxBodyPlaces = 4

// bodyPlaces bounds, per account, how many request bodies are being read at
// once. It hangs off Handlers, so every server has its own and the tests can
// build many in one process. The zero value is ready to use.
//
// There is no process-wide place behind the per-account one, and that is the
// point: an account whose bodies trickle in would otherwise hold a place that
// every other account is waiting for. The cost is that the bound on memory
// grows with the number of accounts rather than being one figure.
type bodyPlaces struct {
	places chat.KeyedPlaces
}

// acquire waits for one of the account's places until the request's context
// ends. For HTTP/1.1, net/http cancels that context on a disconnect only once the
// body has reached EOF, so a client that vanished before sending its body is not
// released from the queue: it waits for a place like any other request, and its
// first read fails at once. The wait is bounded by the holders' windows, not a
// leak.
func (p *bodyPlaces) acquire(ctx context.Context, account string) (func(), error) {
	return p.places.Acquire(ctx, account, maxBodyPlaces)
}

// bodyReadWindow is how long a request body may take to arrive once its place
// is granted. It is cmd/server's ReadTimeout, the window a body gets when
// nothing queues ahead of it. It starts at the grant rather than at the headers
// because the server arms its deadline when the headers are read, so a body that
// queued behind four others would spend its window waiting and be cut off for
// time that was not its own.
const bodyReadWindow = 5 * time.Minute

// claimBody takes one of the account's places before a request body is read.
// The caller gives it back once the body is in and parsed, which is before the
// request is authorised or sent upstream, so no generation holds a place.
//
// The body's read window is armed at the grant. See the release for when it is
// lifted.
func (h *Handlers) claimBody(w http.ResponseWriter, r *http.Request, who caller) (func(), error) {
	release, err := h.bodies.acquire(r.Context(), who.account.ID)
	if err != nil {
		// The request's context ended while it waited, so nobody is left to read
		// the answer.
		return nil, apiError{
			status:  statusClientClosedRequest,
			kind:    "invalid_request_error",
			code:    "cancelled",
			message: "The request was cancelled.",
		}
	}
	window := h.bodyWindow
	if window == 0 {
		window = bodyReadWindow
	}
	// The same window the panel's uploads get, lifted by the same rule: only
	// once the body has reached EOF. See chat.BoundBodyRead.
	lift := chat.BoundBodyRead(w, r, window)
	return func() {
		lift()
		release()
	}, nil
}
