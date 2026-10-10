package compat

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
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
//
// This is a mutex and not a row lock, for the reason the attachment endpoints
// give: what is bounded is this process's memory, not an invariant in the
// database. A second instance against the same database has its own map.
type bodyPlaces struct {
	mu      sync.Mutex
	holders map[string]*accountPlaces
}

// accountPlaces is one account's places. places holds a token for each body
// being read, so its length is the number in use. refs counts the requests
// that hold a place or queue for one; the entry leaves the map when that
// reaches zero, so the map holds only accounts that are reading or waiting.
type accountPlaces struct {
	places chan struct{}
	refs   int
}

// acquire waits for one of the account's places until the request's context
// ends. For HTTP/1.1, net/http cancels that context on a disconnect only once the
// body has reached EOF, so a client that vanished before sending its body is not
// released from the queue: it waits for a place like any other request, and its
// first read fails at once. The wait is bounded by the holders' windows, not a
// leak. The release gives the place back and is idempotent, so a path can give
// it back early and still defer it.
func (p *bodyPlaces) acquire(ctx context.Context, account string) (func(), error) {
	p.mu.Lock()
	if p.holders == nil {
		p.holders = make(map[string]*accountPlaces)
	}
	entry := p.holders[account]
	if entry == nil {
		entry = &accountPlaces{places: make(chan struct{}, maxBodyPlaces)}
		p.holders[account] = entry
	}
	// Counted under the same lock as the lookup and before the wait, so a
	// request leaving in between cannot drop an entry this one is about to
	// queue on.
	entry.refs++
	p.mu.Unlock()

	leave := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(p.holders, account)
		}
	}

	select {
	case entry.places <- struct{}{}:
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-entry.places
			leave()
		})
	}, nil
}

// bodyReadWindow is how long a request body may take to arrive once its place
// is granted. It is cmd/server's ReadTimeout, the window a body gets when
// nothing queues ahead of it. It starts at the grant rather than at the headers
// because the server arms its deadline when the headers are read, so a body that
// queued behind four others would spend its window waiting and be cut off for
// time that was not its own.
const bodyReadWindow = 5 * time.Minute

// trackedBody is the request body as the handler reads it. It records whether a
// read reached the end, which decides whether the window is lifted when the
// place is given back.
type trackedBody struct {
	io.ReadCloser
	sawEOF bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.sawEOF = true
	}
	return n, err
}

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
			status:  499,
			kind:    "invalid_request_error",
			code:    "cancelled",
			message: "The request was cancelled.",
		}
	}
	window := h.bodyWindow
	if window == 0 {
		window = bodyReadWindow
	}
	// Best effort, as boundBodyRead is: a ResponseWriter that cannot set a
	// deadline keeps the server's own.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(window))
	body := &trackedBody{ReadCloser: r.Body}
	r.Body = body
	return func() {
		// Lifted only once the body has reached EOF, which is when net/http starts
		// its background read. A deadline still running then would be taken for
		// the client going away and cancel the generation. A body left short keeps
		// its deadline: net/http reads what is left of it before it writes the
		// response headers, and with no deadline that read waits on a client that
		// may never send the rest.
		if body.sawEOF {
			_ = controller.SetReadDeadline(time.Time{})
		}
		release()
	}, nil
}
