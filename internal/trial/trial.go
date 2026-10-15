// Package trial is the front door's sample conversation: a visitor with no
// account asking the instance a couple of questions before deciding whether
// to sign up.
//
// It is deliberately not the chat gateway with the authentication removed.
// Nothing here is stored — no conversation, no message, no attachment, no
// ledger row — because there is no account to store it against, and an
// anonymous transcript is a liability that has to be retained, secured and
// eventually deleted for no benefit. The whole exchange lives in the request
// body: the client sends what has been said so far, the server answers, and
// forgets.
//
// That shape moves the turn limit into the server's hands rather than the
// browser's: the cap is enforced by counting the messages in the request, so
// a client that lies about how many turns it has had is refused rather than
// obeyed.
//
// Every trial turn is spent from the operator's own provider credit by
// someone who has not identified themselves, which is why this file has a
// per-address budget of its own and why the feature is off by default.
//
// Both budgets live in this process's memory (see budget for why). With N
// instances on one database each keeps its own, so the instance ceiling bounds
// N times the provider credit it names. The trial is meant to run on a single
// instance. The check-then-write is held by a mutex rather than a database row
// lock, as the login limiter's is, and that is a decision, not an oversight.
package trial

import (
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

const (
	// The longest a single trial message may be. Generous for a question,
	// far short of pasting a codebase in.
	MaxMessageChars = 4000
	// The whole exchange, so a long conversation cannot be replayed as one
	// enormous prompt.
	MaxTotalChars = 24000

	// Turns one address may spend per window. A person trying the instance
	// out uses a handful; anything past this is not evaluating the product.
	burstPerAddress = 20
	budgetWindow    = time.Hour

	// The whole front door's spend per window, across every address this
	// process serves.
	//
	// The per-address budget assumes an address means something. Behind a
	// misconfigured proxy, or against a botnet, it does not — so this is the
	// number that actually bounds what the front door can cost, and it holds
	// however many addresses show up. It is per process: see the package
	// comment on running the trial on one instance.
	burstPerInstance = 240

	// Concurrent generations the trial may hold open at once. Not a cost
	// bound — that is the two above — but a bound on upstream connections, so
	// a burst cannot starve the accounts that are paying for this instance.
	maxConcurrent = 6

	// A trial answer is a sample, not a document. Independent of what the
	// model declares, because the model is the operator's choice and this
	// ceiling is about what a stranger may ask them to buy.
	MaxTrialOutputTokens = 600
	// How often dead entries are swept. Bounded work on the write path, so
	// there is no timer goroutine for it.
	sweepInterval = time.Minute
	// Addresses remembered at once. Each entry is paid for by an admitted
	// turn, so burstPerInstance already bounds the map; this is the ceiling
	// that still holds if that number is ever raised or the order of the
	// checks below changes.
	maxWindows = 4096
)

// budget is a fixed-window counter per address.
//
// In memory, for the same reason the login limiter is: a counter table would
// turn every trial turn into a database write, and the alternative to that is
// a cache tier this project does not want. A restart forgives outstanding
// counts, which is an acceptable trade for one binary.
type budget struct {
	mu        sync.Mutex
	windows   map[string]*window
	lastSwept time.Time
	// Every address together. Kept in the same lock as the per-address
	// windows so the two cannot disagree about what has been spent.
	instance *window
	inFlight int
}

type window struct {
	spent   int
	startAt time.Time
}

func newBudget() *budget {
	now := time.Now()
	return &budget{
		windows:   map[string]*window{},
		lastSwept: now,
		instance:  &window{startAt: now},
	}
}

// budgetKey is the window a turn is charged to. Charge and refund both go
// through it, so they cannot disagree about which window they touched.
func budgetKey(address string) string {
	key := strings.TrimSpace(address)
	if key == "" {
		// An address the proxy configuration did not give us. Counting these
		// together is stricter than letting them through uncounted.
		key = "unknown"
	}

	// One /64 is one visitor, however many addresses inside it they hold.
	return httpx.RateKey(key)
}

// take records one turn against an address and reports whether it was within
// budget, plus how long until the window rolls over.
func (b *budget) take(address string) (bool, time.Duration) {
	key := budgetKey(address)

	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()

	if now.Sub(b.lastSwept) > sweepInterval {
		b.sweepLocked(now)
	}

	if now.Sub(b.instance.startAt) > budgetWindow {
		b.instance = &window{startAt: now}
	}
	// Checked before anything is allocated for the caller: when the instance
	// ceiling is reached, whose turn it was does not matter, and a refused
	// request must not leave an entry behind or the refusals themselves fill
	// the map.
	if b.instance.spent >= burstPerInstance {
		return false, budgetWindow - now.Sub(b.instance.startAt)
	}

	entry, ok := b.windows[key]
	if !ok || now.Sub(entry.startAt) > budgetWindow {
		if !ok && len(b.windows) >= maxWindows {
			b.sweepLocked(now)
			if len(b.windows) >= maxWindows {
				return false, sweepInterval
			}
		}
		entry = &window{startAt: now}
		b.windows[key] = entry
	}
	if entry.spent >= burstPerAddress {
		return false, budgetWindow - now.Sub(entry.startAt)
	}

	entry.spent++
	b.instance.spent++
	return true, 0
}

// refund gives back a turn take charged, for a request that never reached the
// provider. Both counters share one caveat: if a window rolled over between the
// charge and the refund, the refund lands on whichever window holds the address
// or the instance now, and so can credit a turn that window never charged. Each
// charge is not recorded against its window, because a record per turn would be
// paid on every trial request to correct an error of about one turn an hour.
func (b *budget) refund(address string) {
	key := budgetKey(address)

	b.mu.Lock()
	defer b.mu.Unlock()

	if entry, ok := b.windows[key]; ok && entry.spent > 0 {
		entry.spent--
	}
	if b.instance.spent > 0 {
		b.instance.spent--
	}
}

func (b *budget) sweepLocked(now time.Time) {
	for candidate, entry := range b.windows {
		if now.Sub(entry.startAt) > budgetWindow {
			delete(b.windows, candidate)
		}
	}
	b.lastSwept = now
}

// enter claims one of the concurrent slots. The release is idempotent.
func (b *budget) enter() (func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inFlight >= maxConcurrent {
		return nil, false
	}
	b.inFlight++

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.inFlight--
			b.mu.Unlock()
		})
	}, true
}

// refusal says why admit turned a turn away.
type refusal int

const (
	admitted refusal = iota
	refusedBusy
	refusedExhausted
)

// admit claims a concurrent slot, then charges the address. The order is the
// point: a turn turned away for want of a slot never reaches the provider, so
// it must not spend the address's or the instance's allowance, or a burst that
// overflows the slots would drain the hourly ceiling for every visitor.
func (b *budget) admit(address string) (func(), time.Duration, refusal) {
	release, ok := b.enter()
	if !ok {
		return nil, 0, refusedBusy
	}
	if ok, retryAfter := b.take(address); !ok {
		release()
		return nil, retryAfter, refusedExhausted
	}
	return release, 0, admitted
}
