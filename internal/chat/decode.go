package chat

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// DecodeGate bounds how many request bodies are being read at once. A request
// claims its account's place first and then one of the global places, and gives
// both back when its body is in: whatever the handler does with the body after
// that is not memory this gate protects, and holding a place through it would
// stop everybody's uploads for as long as a generation runs.
//
// It is one gate per server rather than a package global, because the tests
// build many servers in one process. The server hands the same gate to the /v1
// endpoints, so the bound holds for the process whichever door a body came in by.
type DecodeGate struct {
	// Places in use by request bodies, one per body being read.
	decoding chan struct{}
	// One place per account, claimed before one of the global places. See
	// accountSlots.
	accountDecoding accountSlots
}

// NewDecodeGate returns a gate with the given number of global places.
func NewDecodeGate(places int) *DecodeGate {
	return &DecodeGate{decoding: make(chan struct{}, places)}
}

// MaxConcurrentDecodes is how many request bodies may be read into memory at
// once, across every account and every endpoint that takes a large body.
//
// A body being read holds its encoded bytes, and an upload or a picture also
// its decoded copy, so each one costs several times its size while it runs.
// The stored caps say nothing about that: they are checked after the
// allocation. Small enough that a burst cannot exhaust memory, large enough
// that nobody sharing an instance waits behind one.
const MaxConcurrentDecodes = 4

// How long a body may take to arrive while it holds one of those places. The
// place is claimed before the first byte is read, so a client that sends its
// headers and then trickles would otherwise sit on it for the server's whole
// five-minute read timeout; a few of them would stop everybody's uploads.
const decodeReadWindow = 2 * time.Minute

// accountSlots is the per-account half of the decoding bound. An account gets
// one place at a time, so its own queue waits here rather than on the global
// places, where it would take the places every other account is waiting for.
//
// This is a mutex and not a row lock: what is being bounded is this process's
// memory, not an invariant in the database. A second instance against the same
// database has its own map and its own global places, the same scope the
// global bound has.
type accountSlots struct {
	mu      sync.Mutex
	holders map[string]*accountSlot
}

// accountSlot is one account's place. refs counts the request holding the place
// and every request queued behind it; the entry leaves the map when that reaches
// zero, so the map holds only accounts that are decoding or waiting.
type accountSlot struct {
	slot chan struct{}
	refs int
}

// acquire waits for the account's place for as long as the request is still
// wanted, exactly as the global wait does. The release is idempotent.
func (s *accountSlots) acquire(ctx context.Context, account string) (func(), error) {
	s.mu.Lock()
	// The zero value is ready to use, so NewHandlers does not need to know this
	// map exists.
	if s.holders == nil {
		s.holders = make(map[string]*accountSlot)
	}
	entry := s.holders[account]
	if entry == nil {
		entry = &accountSlot{slot: make(chan struct{}, 1)}
		s.holders[account] = entry
	}
	// Taken under the same lock as the lookup and before the wait, so a holder
	// releasing in between cannot drop an entry this request is about to queue on.
	entry.refs++
	s.mu.Unlock()

	leave := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.holders, account)
		}
	}

	select {
	case entry.slot <- struct{}{}:
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-entry.slot
			leave()
		})
	}, nil
}

// Acquire claims the account's place and then one of the global places,
// waiting for each for as long as the request is still wanted. The release
// gives both back and is idempotent, so a path can hand them back early and
// still defer it.
func (g *DecodeGate) Acquire(ctx context.Context, account string) (func(), error) {
	// The account's place comes first, so a request queued behind its own
	// account holds no global slot while it waits.
	releaseAccount, err := g.accountDecoding.acquire(ctx, account)
	if err != nil {
		return nil, err
	}
	select {
	case g.decoding <- struct{}{}:
	case <-ctx.Done():
		releaseAccount()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.decoding
			releaseAccount()
		})
	}, nil
}

// BoundBodyRead gives the body a deadline while a decoding place is held, and
// returns what lifts it once the body is in. Lifted rather than left to
// expire: the server reads in the background to notice a client that has gone,
// and a deadline that passes while the handler is still working is reported
// there as a disconnect, cancelling the request in the middle of whatever it
// was doing. Best effort — a ResponseWriter that cannot set a deadline keeps
// the server-wide timeout and the returned function does nothing.
func BoundBodyRead(w http.ResponseWriter) (bodyRead func()) {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(decodeReadWindow))
	return func() { _ = controller.SetReadDeadline(time.Time{}) }
}
