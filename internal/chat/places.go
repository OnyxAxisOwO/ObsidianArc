package chat

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// KeyedPlaces bounds how many requests one key — an account — may have in a
// stage at once, with a queue per key so one account waiting on itself never
// takes a place another account is waiting for. The zero value is ready to
// use, so a Handlers does not need a constructor to know it exists.
//
// The decoding bound here and the /v1 body bound in internal/compat both use
// it. They were two copies of the same map, refcount and release once, and a
// fix to one had to be remembered in the other.
//
// This is a mutex and not a row lock, for the reason uploadAttachment gives for
// the global slots: what is bounded is this process's memory, not an invariant
// in the database. A second instance against the same database has its own
// map, the same scope the bound has.
type KeyedPlaces struct {
	mu      sync.Mutex
	holders map[string]*keyedPlace
}

// keyedPlace is one key's places. places holds a token for each one in use,
// so its length is the number taken. refs counts the requests holding a place
// and every request queued behind them; the entry leaves the map when that
// reaches zero, so the map holds only keys that are busy or waiting.
type keyedPlace struct {
	places chan struct{}
	refs   int
}

// Acquire waits for one of key's per places for as long as ctx lasts. per is
// read when the key's entry is made, so every caller of one KeyedPlaces passes
// the same figure. The release is idempotent, so a path can give the place
// back early and still defer it.
func (p *KeyedPlaces) Acquire(ctx context.Context, key string, per int) (func(), error) {
	p.mu.Lock()
	if p.holders == nil {
		p.holders = make(map[string]*keyedPlace)
	}
	entry := p.holders[key]
	if entry == nil {
		entry = &keyedPlace{places: make(chan struct{}, max(1, per))}
		p.holders[key] = entry
	}
	// Counted under the same lock as the lookup and before the wait, so a
	// holder releasing in between cannot drop an entry this request is about
	// to queue on.
	entry.refs++
	p.mu.Unlock()

	leave := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(p.holders, key)
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

// State reports how many of key's places are taken and how many requests hold
// or wait for one. It is for tests, which hold the map to its bounds.
func (p *KeyedPlaces) State(key string) (inUse, refs int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.holders[key]
	if entry == nil {
		return 0, 0
	}
	return len(entry.places), entry.refs
}

// Tracked is how many keys the map still has an entry for. Once every request
// has ended it must be zero, or the map grows with every key it ever saw.
func (p *KeyedPlaces) Tracked() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.holders)
}

// BoundBodyRead gives the request body a read deadline window from now, and
// returns what lifts it once the body is in. Used where a place is held while
// the body arrives, so a client that sends its headers and then trickles
// cannot sit on the place for the server's whole read timeout. Best effort: a
// ResponseWriter that cannot set a deadline keeps the server-wide one and the
// returned function does nothing.
//
// Lifted rather than left to expire: the server reads in the background to
// notice a client that has gone, and a deadline that passes while the handler
// is still working is reported there as a disconnect, cancelling the request
// in the middle of whatever it was doing.
//
// It is lifted only once the body has reached EOF. A body left short — refused
// as too large, or not valid JSON, or abandoned by a client that stopped
// sending — keeps it: net/http reads what is left of an unread body before it
// writes the response, and with no deadline that read waits forever on a
// client that never sends the rest, holding the connection and its goroutine.
// A body read to the end has nothing left for that read to wait on.
func BoundBodyRead(w http.ResponseWriter, r *http.Request, window time.Duration) (lift func()) {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(window))
	body := &eofBody{ReadCloser: r.Body}
	r.Body = body
	return func() {
		if body.sawEOF {
			_ = controller.SetReadDeadline(time.Time{})
		}
	}
}

// eofBody is a request body that remembers whether a read reached its end.
type eofBody struct {
	io.ReadCloser
	sawEOF bool
}

func (b *eofBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.sawEOF = true
	}
	return n, err
}
