package quota

import (
	"context"
	"sync"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
)

// funding is what bonus bars paid of one reservation.
type funding struct {
	mu    sync.Mutex
	holds []bonus.Hold
	// Whether the windows were reserved against as well, which is when the
	// bars covered only part.
	windows  bool
	released bool
}

// The reservation is made when a turn is authorised and settled when it is
// recorded, in two places that share nothing but the request they belong to.
// A ledger in the request's context is how the second finds the first: one per
// request, made where the request enters, so nothing a client sends can make
// two requests share one.
type ledgerKey struct{}

type ledger struct {
	mu      sync.Mutex
	pending []*funding
}

// WithLedger is the context a request runs in.
func WithLedger(ctx context.Context) context.Context {
	return context.WithValue(ctx, ledgerKey{}, &ledger{})
}

// noted files the funding of a reservation, if it has any and there is a
// ledger to file it in, and hands the reservation back.
func (s *Service) noted(ctx context.Context, res Reservation) Reservation {
	if res.funding == nil {
		return res
	}
	if l, ok := ctx.Value(ledgerKey{}).(*ledger); ok {
		l.mu.Lock()
		l.pending = append(l.pending, res.funding)
		l.mu.Unlock()
	}
	return res
}

// takeFunding is the oldest funding noted in the request and not yet settled.
func takeFunding(ctx context.Context) *funding {
	l, ok := ctx.Value(ledgerKey{}).(*ledger)
	if !ok {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pending) == 0 {
		return nil
	}
	f := l.pending[0]
	l.pending = l.pending[1:]
	return f
}
